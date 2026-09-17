package config

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/openai/openai-go/v3/option"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/applog"
	"lemmary/backend/internal/chatgpt"
	"lemmary/backend/internal/ocr"
	"lemmary/backend/internal/websearch"
)

type Snapshot struct {
	Cfg         Config
	OCR         ocr.Provider
	AI          ai.Extractor
	Chatter     ai.Chatter
	SearchAgent ai.SearchAgent
	// SearchHelper does Deep Search's bulk per-document work. Nil only when the
	// search agent itself is unavailable; otherwise set on the helper binding or,
	// through the fallback chain, on the search model.
	SearchHelper ai.Helper
	// Shares the extraction provider: both reason over document text.
	Splitter ai.Splitter
	// Embedder is nil unless an embedding model is bound, which is what turns dense
	// retrieval on. Consumers check for nil and degrade to keyword search.
	Embedder ai.Embedder
	// WebSearch is nil unless a web-search provider is bound. It is what makes
	// the web_search and web_fetch tools offerable at all; a user still has to
	// ask for them on the turn.
	WebSearch *websearch.Tavily
}

type Runtime struct {
	// Serializes whole Reload calls: two closely-spaced saves can otherwise race and
	// the goroutine that read the older record may publish last.
	reloadMu sync.Mutex
	mu       sync.RWMutex
	snap     Snapshot

	// Parsed once before the app exists and never changes, so no lock. Rides here
	// because Runtime already reaches the refuse-write endpoints and /meta.
	env AIEnv

	// Outside the snapshot on purpose: it caches what a third party answered,
	// and a settings save must not throw that away.
	catalog *aiprovider.Catalog

	// Called after every published snapshot, in registration order.
	onReload []func(core.App, Snapshot)
}

// OnReload is for state derived from the configuration but not living in the
// snapshot: the vector index, whose mapping depends on the embedding model and
// on a dimension count only known once a provider has answered. A callback runs
// inside the reload, so it must schedule the slow half rather than do it.
func (r *Runtime) OnReload(fn func(core.App, Snapshot)) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.onReload = append(r.onReload, fn)
	r.mu.Unlock()
}

func NewRuntime(env AIEnv) *Runtime {
	// AI_MANAGED is read once here and handed to the package that owns the document
	// header: see aiprovider.SetManaged.
	aiprovider.SetManaged(env.Managed)
	return &Runtime{
		snap: Snapshot{Cfg: env.Defaults()},
		env:  env,
		// slog's default rather than app.Logger(): this is built before the app
		// exists, and swapping the logger in later would race every lookup.
		catalog: aiprovider.NewCatalog(env.ModelCatalogURL, slog.Default().With("component", "ai")),
	}
}

// ModelCatalog answers how large a model's context window is, for the code that
// reports a turn against it.
func (r *Runtime) ModelCatalog() *aiprovider.Catalog { return r.catalog }

func (r *Runtime) Env() AIEnv { return r.env }

func (r *Runtime) Managed() bool { return r.env.Managed }

// AlwaysRequireReview comes off the snapshot rather than the env, unlike
// Managed: it is a tenant's own setting, so it changes when Settings is saved
// and the runtime reloads.
func (r *Runtime) AlwaysRequireReview() bool { return r.Snapshot().Cfg.AlwaysRequireReview }

// WebSearchAvailable is read by /meta, which is how the SPA knows whether to
// offer the web toggle in a chat. Off the snapshot for the same reason
// AlwaysRequireReview is: binding a provider changes it without a restart.
func (r *Runtime) WebSearchAvailable() bool { return r.Snapshot().WebSearch != nil }

func (r *Runtime) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap
}

// Reload rebuilds OCR/AI clients from the DB. Unavailable settings fall back to
// env defaults; missing keys soft-fail so the process stays up.
func (r *Runtime) Reload(app core.App) error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	cfg, err := Load(app)
	if err != nil {
		app.Logger().Warn("loading app_settings failed; using env defaults", slog.Any("error", err))
		cfg = r.env.Defaults()
	}
	r.apply(app, cfg)
	return nil
}

// usableLLM asks Configured rather than APIKey != "": the chatgpt SDK holds no
// key, and the old question left every chatgpt binding silently unavailable.
func usableLLM(p *aiprovider.Provider) bool {
	return p != nil && p.Configured() && aiprovider.IsLLM(p.SDK)
}

// providerCredential is the key to pass plus any extra SDK options, for both the
// AI clients and the OCR one. Every SDK but one hands over its API key and asks
// for nothing else; the chatgpt SDK has no key, its credential being a token
// that expires hourly, so it contributes a middleware that mints one per request
// plus a placeholder for the SDK's insistence on a non-empty key.
func providerCredential(app core.App, p *aiprovider.Provider, logger *slog.Logger) (string, []option.RequestOption) {
	if p == nil {
		return "", nil
	}
	if !aiprovider.RequiresOAuth(p.SDK) {
		return p.APIKey, nil
	}
	src := chatgpt.SourceFor(p.ID, p.OAuth, persistOAuth(app), logger)
	return chatgpt.PlaceholderKey, []option.RequestOption{
		option.WithMiddleware(chatgpt.Middleware(src, logger)),
	}
}

// persistOAuth saves through the record so the value passes the same field
// validation as any other write. That fires the ai_providers update hook, which
// is why the hook skips the reload when oauth alone moved: see reloadProviders.
func persistOAuth(app core.App) chatgpt.Persist {
	return func(providerID, oauth string) error {
		record, err := app.FindRecordById(aiprovider.CollectionName, providerID)
		if err != nil {
			return err
		}
		record.Set(aiprovider.OAuthField, oauth)
		return app.Save(record)
	}
}

func (r *Runtime) apply(app core.App, cfg Config) {
	logger := app.Logger()
	ocrLogger := logger.With("component", "ocr")
	aiLogger := logger.With("component", "ai")

	// Every client is built by the same function an override goes through, in
	// override.go, so a per-request client is identical but for its model: the
	// credential handling, middleware and timeouts are not restated here.
	ocrProvider, err := buildOCR(app, cfg, cfg.OCRProvider, cfg.OCRModel, ocrLogger)
	if err != nil {
		logger.Warn("OCR provider unavailable after settings reload", slog.Any("error", err))
		ocrProvider = nil
	}
	extractor, splitter := buildExtractPair(app, cfg, cfg.ExtractProvider, cfg.ExtractModel, aiLogger)
	chatter := buildChatter(app, cfg, cfg.ExtractProvider, cfg.ExtractModel, aiLogger)
	embedder := buildEmbedder(app, cfg, cfg.EmbeddingProvider, cfg.EmbeddingModel, aiLogger)
	// The general binding. A research turn rebuilds this on the research binding
	// through WithOverrides; see appapi/search.go.
	searchAgent := buildSearchAgent(app, cfg, cfg.ExtractProvider, cfg.ExtractModel, aiLogger)
	searchHelper := buildHelper(app, cfg, cfg.ExtractProvider, cfg.ExtractModel, aiLogger)
	webSearch := buildWebSearch(app, cfg, cfg.WebSearchProvider, aiLogger)

	snap := Snapshot{
		Cfg:          cfg,
		OCR:          ocrProvider,
		AI:           extractor,
		Chatter:      chatter,
		SearchAgent:  searchAgent,
		SearchHelper: searchHelper,
		Splitter:     splitter,
		Embedder:     embedder,
		WebSearch:    webSearch,
	}

	r.mu.Lock()
	r.snap = snap
	callbacks := make([]func(core.App, Snapshot), len(r.onReload))
	copy(callbacks, r.onReload)
	r.mu.Unlock()

	// Outside the lock: a callback reaching back for the snapshot it was just
	// handed would deadlock.
	for _, fn := range callbacks {
		fn(app, snap)
	}

	// Logged from the published snapshot, not the locals, so the line describes
	// what readers will actually get.
	ocrName := "unavailable"
	if snap.OCR != nil {
		ocrName = snap.OCR.Name()
	}
	aiName := "unavailable"
	aiModel := ""
	if snap.AI != nil {
		aiName = snap.AI.Name()
		aiModel = snap.AI.Model()
	}
	logger.Info("runtime settings reloaded",
		"ocr", ocrName,
		"ocr_model", cfg.OCRModel,
		"ai", aiName,
		"model", aiModel,
		"research_model", cfg.ResearchModel,
		"embedding_model", cfg.EmbeddingModel,
		"embedding_dims", cfg.EmbeddingDims,
		"deep_search_languages", cfg.DeepSearchLanguages,
	)
}

// Bootstrap never fails due to settings: the app must start so admins can open
// Settings.
func RegisterHooks(app core.App, rt *Runtime) {
	// High-priority hook, so the stdout tee is in place before other OnBootstrap
	// handlers unwind and log, possibly from goroutines.
	applog.Register(app)

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}

		// serve does not apply app migrations automatically.
		if err := e.App.RunAppMigrations(); err != nil {
			e.App.Logger().Warn("app migrations failed", slog.Any("error", err))
		}

		if err := EnsureDefaults(e.App, rt.env); err != nil {
			e.App.Logger().Warn("ensure app_settings defaults failed; continuing with env fallback", slog.Any("error", err))
		}

		// After seeding, before Reload, so a recreated container serves the new
		// environment on its first request. Fail the boot rather than warn: on a
		// managed instance nobody inside can repair a failed rewrite.
		if rt.env.Managed {
			if err := ApplyManaged(e.App, rt.env); err != nil {
				return fmt.Errorf("apply managed AI configuration: %w", err)
			}
		}

		_ = rt.Reload(e.App)
		return nil
	})

	reloadSettings := func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if e.Record.Id != SingletonID {
			return nil
		}
		_ = rt.Reload(e.App)
		return nil
	}

	app.OnRecordAfterCreateSuccess(CollectionName).BindFunc(reloadSettings)
	app.OnRecordAfterUpdateSuccess(CollectionName).BindFunc(reloadSettings)

	reloadProviders := func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		_ = rt.Reload(e.App)
		return nil
	}
	updateProviders := func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if onlyTokenRotated(e.Record) {
			return nil
		}
		_ = rt.Reload(e.App)
		return nil
	}
	deleteProviders := func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		// A deleted row's token source would otherwise sit in the package registry
		// until restart, still holding a refresh token for an unreachable provider.
		if e.Record != nil && aiprovider.RequiresOAuth(e.Record.GetString("sdk")) {
			chatgpt.Forget(e.Record.Id)
		}
		_ = rt.Reload(e.App)
		return nil
	}
	app.OnRecordAfterCreateSuccess(aiprovider.CollectionName).BindFunc(reloadProviders)
	app.OnRecordAfterUpdateSuccess(aiprovider.CollectionName).BindFunc(updateProviders)
	app.OnRecordAfterDeleteSuccess(aiprovider.CollectionName).BindFunc(deleteProviders)
}

// onlyTokenRotated keeps the hourly ChatGPT token refresh, which saves the
// provider row, from rebuilding every AI client on a timer: that would swap the
// extractor out from under a running job and re-read a row the token source has
// just written.
//
// A rotation, not merely "oauth moved". Sign-in and sign-out touch no other
// field either and both change what the row can serve, so the test is that the
// row was usable before and stays usable after.
func onlyTokenRotated(record *core.Record) bool {
	if record == nil {
		return false
	}
	original := record.Original()
	if original == nil {
		return false
	}
	before := strings.TrimSpace(original.GetString(aiprovider.OAuthField))
	after := strings.TrimSpace(record.GetString(aiprovider.OAuthField))
	if before == after || before == "" || after == "" {
		return false
	}
	for _, field := range []string{"sdk", "alias", "base_url", "api_key"} {
		if record.GetString(field) != original.GetString(field) {
			return false
		}
	}
	return true
}
