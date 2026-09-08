package config

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/openai/openai-go/option"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/applog"
	"lemmary/backend/internal/chatgpt"
	"lemmary/backend/internal/ocr"
)

type Snapshot struct {
	Cfg         Config
	OCR         ocr.Provider
	AI          ai.Extractor
	Chatter     ai.Chatter
	SearchAgent ai.SearchAgent
	// SearchHelper does Deep Search's bulk per-document work. Nil when the
	// search agent itself is unavailable; otherwise always set, on the helper
	// binding or, through the fallback chain, on the search model.
	SearchHelper ai.Helper
	// Shares the extraction provider: both reason over document text.
	Splitter ai.Splitter
	// Embedder is nil unless an embedding model is bound, which is what turns
	// dense retrieval on: every consumer checks for nil and degrades to
	// keyword search rather than failing.
	Embedder ai.Embedder
}

type Runtime struct {
	// Serializes whole Reload calls. Without it, two closely-spaced saves can
	// race and the goroutine that read the older record may publish last.
	reloadMu sync.Mutex
	mu       sync.RWMutex
	snap     Snapshot

	// Parsed once before the app exists; never changes, so no lock. Rides here
	// because Runtime is already threaded to the refuse-write endpoints and /meta.
	env AIEnv

	// Called after every published snapshot, in registration order.
	onReload []func(core.App, Snapshot)
}

// OnReload registers a callback for every settings reload.
//
// It exists for state that is derived from the configuration but does not live
// in the snapshot — the vector index, whose mapping depends on the embedding
// model and on a dimension count that is only known once a provider has
// answered. A callback runs inside the reload, so it must be quick: schedule
// the slow half rather than doing it here.
func (r *Runtime) OnReload(fn func(core.App, Snapshot)) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.onReload = append(r.onReload, fn)
	r.mu.Unlock()
}

func NewRuntime(env AIEnv) *Runtime {
	return &Runtime{
		snap: Snapshot{Cfg: env.Defaults()},
		env:  env,
	}
}

func (r *Runtime) Env() AIEnv { return r.env }

func (r *Runtime) Managed() bool { return r.env.Managed }

// ChatGPTLogin reports whether the chatgpt SDK may be used at all. Read by the
// provider endpoints, which refuse it when off, and by /meta, which is how the
// SPA knows whether to offer the sign-in button.
func (r *Runtime) ChatGPTLogin() bool { return r.env.ChatGPTLogin }

// AlwaysRequireReview reports whether every AI-extracted document waits in the
// review Inbox. Off the snapshot rather than the env, unlike Managed and
// ChatGPTLogin: it is a tenant's own setting, so it changes when Settings is
// saved and the runtime reloads.
func (r *Runtime) AlwaysRequireReview() bool { return r.Snapshot().Cfg.AlwaysRequireReview }

func (r *Runtime) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap
}

// Reload rebuilds OCR/AI clients from the DB. Unavailable settings fall back
// to env defaults; missing keys soft-fail so the process stays up.
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

// usableLLM reports whether a provider row can back a language-model binding.
//
// Configured rather than APIKey != "": the chatgpt SDK holds no key, and asking
// the old question would have left every chatgpt binding silently unavailable
// with nothing in the log but "ai: unavailable".
func usableLLM(p *aiprovider.Provider) bool {
	return p != nil && p.Configured() && aiprovider.IsLLM(p.SDK)
}

// providerCredential is what a client on a provider row is built with: the key
// to pass, and any extra SDK options the provider needs. Both the AI clients
// and the OCR one go through it.
//
// Every SDK but one hands over its API key and asks for nothing else. The
// chatgpt SDK has no key to hand over -- its credential is a token that expires
// hourly -- so it contributes a middleware that mints one per request instead,
// plus a placeholder for the SDK's own insistence on a non-empty key.
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

// persistOAuth stores a rotated token back on the provider row.
//
// Saved through the record so the value passes the same field validation as any
// other write. It fires the ai_providers update hook, which is why that hook
// skips the reload when oauth is the only field that moved: see reloadProviders.
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

	var ocrProvider ocr.Provider
	if cfg.OCRProvider != nil {
		// The same credential treatment the AI clients get: a chatgpt row has
		// a minted token rather than a key, and the row itself carries neither.
		p := *cfg.OCRProvider
		key, opts := providerCredential(app, cfg.OCRProvider, ocrLogger)
		p.APIKey = key
		built, err := ocr.NewFromAIProvider(p, cfg.OCRModel, cfg.OCRTimeout, ocrLogger, opts...)
		if err != nil {
			logger.Warn("OCR provider unavailable after settings reload", slog.Any("error", err))
		} else {
			ocrProvider = built
		}
	}

	// One credential for the extraction provider, shared by the two clients
	// built on it: asking twice would put two middlewares over one token
	// source, and the splitter is always the extractor's provider.
	extractKey, extractOpts := providerCredential(app, cfg.ExtractProvider, aiLogger)

	var extractor ai.Extractor
	if usableLLM(cfg.ExtractProvider) {
		extractor = ai.NewExtractor(
			cfg.ExtractProvider.SDK,
			extractKey,
			cfg.ExtractModel,
			cfg.ExtractProvider.BaseURL,
			cfg.ExtractionPromptVer,
			cfg.ProcessingResultLanguage,
			cfg.OpenAITimeout,
			aiLogger,
			extractOpts...,
		)
	}

	var chatter ai.Chatter
	if usableLLM(cfg.ChatProvider) {
		key, opts := providerCredential(app, cfg.ChatProvider, aiLogger)
		chatter = ai.NewChatter(
			cfg.ChatProvider.SDK,
			key,
			cfg.ChatModel,
			cfg.ChatProvider.BaseURL,
			cfg.OpenAITimeout,
			aiLogger,
			opts...,
		)
	}

	var splitter ai.Splitter
	if usableLLM(cfg.ExtractProvider) {
		splitter = ai.NewSplitter(
			cfg.ExtractProvider.SDK,
			extractKey,
			cfg.ExtractModel,
			cfg.ExtractProvider.BaseURL,
			cfg.OpenAITimeout,
			aiLogger,
			extractOpts...,
		)
	}

	var embedder ai.Embedder
	if HasEmbedding(cfg) {
		embedder = ai.NewEmbedder(
			cfg.EmbeddingProvider.SDK,
			cfg.EmbeddingProvider.APIKey,
			cfg.EmbeddingModel,
			cfg.EmbeddingProvider.BaseURL,
			cfg.EmbeddingDims,
			cfg.OpenAITimeout,
			aiLogger,
		)
	}

	var searchAgent ai.SearchAgent
	if usableLLM(cfg.SearchProvider) {
		key, opts := providerCredential(app, cfg.SearchProvider, aiLogger)
		searchAgent = ai.NewSearchAgent(
			cfg.SearchProvider.SDK,
			key,
			cfg.SearchModel,
			cfg.SearchProvider.BaseURL,
			cfg.OpenAITimeout,
			cfg.DeepSearchLanguages,
			cfg.ProcessingResultLanguage,
			aiLogger,
			opts...,
		)
	}

	var searchHelper ai.Helper
	if usableLLM(cfg.SearchHelperProvider) {
		key, opts := providerCredential(app, cfg.SearchHelperProvider, aiLogger)
		searchHelper = ai.NewHelper(
			cfg.SearchHelperProvider.SDK,
			key,
			cfg.SearchHelperModel,
			cfg.SearchHelperProvider.BaseURL,
			cfg.OpenAITimeout,
			aiLogger,
			opts...,
		)
	}

	snap := Snapshot{
		Cfg:          cfg,
		OCR:          ocrProvider,
		AI:           extractor,
		Chatter:      chatter,
		SearchAgent:  searchAgent,
		SearchHelper: searchHelper,
		Splitter:     splitter,
		Embedder:     embedder,
	}

	r.mu.Lock()
	r.snap = snap
	callbacks := make([]func(core.App, Snapshot), len(r.onReload))
	copy(callbacks, r.onReload)
	r.mu.Unlock()

	// Outside the lock: a callback that reached back for the snapshot it was
	// just handed would otherwise deadlock.
	for _, fn := range callbacks {
		fn(app, snap)
	}

	// Logged from the published snapshot rather than from the locals above, so
	// the line always describes what readers will actually get.
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
		"chat_model", cfg.ChatModel,
		"search_model", cfg.SearchModel,
		"search_helper_model", cfg.SearchHelperModel,
		"embedding_model", cfg.EmbeddingModel,
		"embedding_dims", cfg.EmbeddingDims,
		"deep_search_languages", cfg.DeepSearchLanguages,
	)
}

// Bootstrap never fails due to settings — the app must start so admins can open Settings.
func RegisterHooks(app core.App, rt *Runtime) {
	// High-priority hook so the stdout tee is in place before other
	// OnBootstrap handlers unwind and log (possibly from goroutines).
	applog.Register(app)

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}

		// App migrations are not applied by serve automatically; apply them here.
		if err := e.App.RunAppMigrations(); err != nil {
			e.App.Logger().Warn("app migrations failed", slog.Any("error", err))
		}

		if err := EnsureDefaults(e.App, rt.env); err != nil {
			e.App.Logger().Warn("ensure app_settings defaults failed; continuing with env fallback", slog.Any("error", err))
		}

		// After seeding, before Reload, so a recreated container serves the
		// new environment on its first request.
		//
		// Fail the boot rather than warn: on a managed instance nobody inside
		// can repair a failed rewrite.
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
		// A deleted row's token source would otherwise sit in the package
		// registry until restart, still holding a refresh token for a provider
		// nobody can reach any more.
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

// onlyTokenRotated reports a write that did nothing but replace one live
// ChatGPT token with another.
//
// The token refreshes about once an hour, and every refresh saves the provider
// row. Reloading on those would rebuild every AI client on a timer -- swapping
// the extractor out from under a running job, and re-reading a row the token
// source itself has just written.
//
// A rotation, not merely "oauth moved". Signing in and signing out also touch
// no other field, and both change what the row can serve: after a sign-in the
// clients do not exist yet, because the last apply saw an unconfigured row, and
// after a sign-out the built clients would keep answering from the token still
// held by the middleware, which Forget cannot reach. Both must reload, so the
// test is that the row was usable before and stays usable after.
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
