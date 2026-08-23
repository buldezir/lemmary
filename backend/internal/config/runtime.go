package config

import (
	"log/slog"
	"sync"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/ai"
	"paperless-go/backend/internal/aiprovider"
	"paperless-go/backend/internal/applog"
	"paperless-go/backend/internal/ocr"
)

// Snapshot is an immutable view of the live runtime config and clients.
type Snapshot struct {
	Cfg         Config
	OCR         ocr.Provider
	AI          ai.Extractor
	Chatter     ai.Chatter
	SearchAgent ai.SearchAgent
	// Splitter proposes document boundaries for the split-documents upload. It
	// shares the extraction provider: both reason over document text.
	Splitter ai.Splitter
}

// Runtime holds the process-global reloadable config and provider clients.
type Runtime struct {
	// reloadMu serializes whole Reload calls (DB read + client build +
	// publish). Without it, two closely-spaced settings saves can race and the
	// goroutine that read the older record may publish last, serving a stale
	// provider or API key until the next save.
	reloadMu sync.Mutex
	mu       sync.RWMutex
	snap     Snapshot
}

func NewRuntime() *Runtime {
	return &Runtime{
		snap: Snapshot{Cfg: DefaultsFromEnv()},
	}
}

func (r *Runtime) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap
}

// Reload reads settings from the DB and rebuilds OCR/AI clients.
// If the DB settings are unavailable, falls back to env defaults so the process stays up.
// Missing OCR/AI keys soft-fail: config is still updated and the process stays up.
func (r *Runtime) Reload(app core.App) error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	cfg, err := Load(app)
	if err != nil {
		app.Logger().Warn("loading app_settings failed; using env defaults", slog.Any("error", err))
		cfg = DefaultsFromEnv()
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

	r.mu.Lock()
	r.snap = Snapshot{
		Cfg:         cfg,
		OCR:         ocrProvider,
		AI:          extractor,
		Chatter:     chatter,
		SearchAgent: searchAgent,
		Splitter:    splitter,
	}
	r.mu.Unlock()

	ocrName := "unavailable"
	if ocrProvider != nil {
		ocrName = ocrProvider.Name()
	}
	aiName := "unavailable"
	aiModel := ""
	if extractor != nil {
		aiName = extractor.Name()
		aiModel = extractor.Model()
	}
	logger.Info("runtime settings reloaded",
		"ocr", ocrName,
		"ocr_model", cfg.OCRModel,
		"ai", aiName,
		"model", aiModel,
		"chat_model", cfg.ChatModel,
		"search_model", cfg.SearchModel,
		"deep_search_languages", cfg.DeepSearchLanguages,
	)
}

// RegisterHooks seeds defaults, loads runtime state on bootstrap, and reloads on settings changes.
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

		if err := EnsureDefaults(e.App); err != nil {
			e.App.Logger().Warn("ensure app_settings defaults failed; continuing with env fallback", slog.Any("error", err))
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
