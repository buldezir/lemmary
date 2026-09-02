package config

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/applog"
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

func (r *Runtime) apply(app core.App, cfg Config) {
	logger := app.Logger()
	ocrLogger := logger.With("component", "ocr")
	aiLogger := logger.With("component", "ai")

	var ocrProvider ocr.Provider
	if cfg.OCRProvider != nil {
		built, err := ocr.NewFromAIProvider(*cfg.OCRProvider, cfg.OCRModel, cfg.OCRTimeout, ocrLogger)
		if err != nil {
			logger.Warn("OCR provider unavailable after settings reload", slog.Any("error", err))
		} else {
			ocrProvider = built
		}
	}

	var extractor ai.Extractor
	if cfg.ExtractProvider != nil && cfg.ExtractProvider.APIKey != "" && aiprovider.IsLLM(cfg.ExtractProvider.SDK) {
		extractor = ai.NewExtractor(
			cfg.ExtractProvider.SDK,
			cfg.ExtractProvider.APIKey,
			cfg.ExtractModel,
			cfg.ExtractProvider.BaseURL,
			cfg.ExtractionPromptVer,
			cfg.ProcessingResultLanguage,
			cfg.OpenAITimeout,
			aiLogger,
		)
	}

	var chatter ai.Chatter
	if cfg.ChatProvider != nil && cfg.ChatProvider.APIKey != "" && aiprovider.IsLLM(cfg.ChatProvider.SDK) {
		chatter = ai.NewChatter(
			cfg.ChatProvider.SDK,
			cfg.ChatProvider.APIKey,
			cfg.ChatModel,
			cfg.ChatProvider.BaseURL,
			cfg.OpenAITimeout,
			aiLogger,
		)
	}

	var splitter ai.Splitter
	if cfg.ExtractProvider != nil && cfg.ExtractProvider.APIKey != "" && aiprovider.IsLLM(cfg.ExtractProvider.SDK) {
		splitter = ai.NewSplitter(
			cfg.ExtractProvider.SDK,
			cfg.ExtractProvider.APIKey,
			cfg.ExtractModel,
			cfg.ExtractProvider.BaseURL,
			cfg.OpenAITimeout,
			aiLogger,
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
	if cfg.SearchProvider != nil && cfg.SearchProvider.APIKey != "" && aiprovider.IsLLM(cfg.SearchProvider.SDK) {
		searchAgent = ai.NewSearchAgent(
			cfg.SearchProvider.SDK,
			cfg.SearchProvider.APIKey,
			cfg.SearchModel,
			cfg.SearchProvider.BaseURL,
			cfg.OpenAITimeout,
			cfg.DeepSearchLanguages,
			cfg.ProcessingResultLanguage,
			aiLogger,
		)
	}

	var searchHelper ai.Helper
	if cfg.SearchHelperProvider != nil && cfg.SearchHelperProvider.APIKey != "" && aiprovider.IsLLM(cfg.SearchHelperProvider.SDK) {
		searchHelper = ai.NewHelper(
			cfg.SearchHelperProvider.SDK,
			cfg.SearchHelperProvider.APIKey,
			cfg.SearchHelperModel,
			cfg.SearchHelperProvider.BaseURL,
			cfg.OpenAITimeout,
			aiLogger,
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
	app.OnRecordAfterCreateSuccess(aiprovider.CollectionName).BindFunc(reloadProviders)
	app.OnRecordAfterUpdateSuccess(aiprovider.CollectionName).BindFunc(reloadProviders)
	app.OnRecordAfterDeleteSuccess(aiprovider.CollectionName).BindFunc(reloadProviders)
}
