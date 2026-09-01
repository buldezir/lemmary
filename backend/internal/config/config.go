package config

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/strutil"
)

const (
	CollectionName = "app_settings"
	SingletonID    = "appsettings0001" // must be 15 chars (PocketBase default id rules)
)

type Config struct {
	OCRProviderID     string
	OCRModel          string
	ExtractProviderID string
	ExtractModel      string
	ChatProviderID    string
	ChatModel         string
	SearchProviderID  string
	SearchModel       string

	OCRProvider     *aiprovider.Provider
	ExtractProvider *aiprovider.Provider
	ChatProvider    *aiprovider.Provider
	SearchProvider  *aiprovider.Provider

	OCRTimeout                    time.Duration
	ProcessingResultLanguage      string
	DeepSearchLanguages           string
	SearchContextTokens           int
	OpenAITimeout                 time.Duration
	WorkerCronExpr                string
	WorkerTimeout                 time.Duration
	WorkerMaxRetries              int
	ExtractionPromptVer           string
	NearDuplicateDetectionEnabled bool
	NearDuplicateThreshold        float64
}

const DefaultNearDuplicateThreshold = 0.92

// DefaultSearchContextTokens is the context window assumed when neither the
// admin nor the provider tells us the real one. Research reads documents until
// this budget is spent, so it is the one number that decides how much of the
// archive a single question can draw on.
const DefaultSearchContextTokens = 128000

func WorkerCronFromEnv() string {
	return getEnv("WORKER_CRON_EXPR", "* * * * *")
}

// DefaultStagingMaxBytes is the fallback for StagingMaxBytesFromEnv.
const DefaultStagingMaxBytes int64 = 1 << 30 // 1 GiB

// minStagingMaxBytes keeps a typo from setting a limit no real upload can meet.
const minStagingMaxBytes int64 = 1 << 20 // 1 MiB

// StagingMaxBytesFromEnv is the largest archive an import may stage on disk.
//
// An importer discards an owner's previous staged upload before writing a new
// one, so this is also the disk one account can occupy while it decides whether
// to confirm: the ceiling on that staging area is roughly this times the number
// of accounts. Lower it on a small volume; raise it for libraries whose backup
// runs past a gigabyte.
func StagingMaxBytesFromEnv() int64 {
	return envInt64Default("IMPORT_STAGING_MAX_BYTES", DefaultStagingMaxBytes, minStagingMaxBytes)
}

// findSettingsCollection returns the app_settings collection.
//
// The schema itself is owned by migrations/ — this only reports a clear error
// when they have not run, instead of maintaining a second copy of the field
// list that can drift from the migration ladder.
func findSettingsCollection(app core.App) (*core.Collection, error) {
	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return nil, fmt.Errorf("%s collection is missing; run migrations: %w", CollectionName, err)
	}
	return collection, nil
}

// EnsureDefaults seeds the app_settings singleton from the environment when it
// is missing.
//
// Seeding happens once, on the boot that finds no singleton. After that the
// Settings page owns every value here — including in managed mode, where
// ApplyManaged runs afterwards and takes the parts the operator owns back.
func EnsureDefaults(app core.App, env AIEnv) error {
	if _, err := aiprovider.EnsureCollection(app); err != nil {
		return err
	}

	if record, err := app.FindRecordById(CollectionName, SingletonID); err == nil {
		return bindProviders(app, record)
	}

	collection, err := findSettingsCollection(app)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Id = SingletonID
	record.MarkAsNew()
	applyConfigToRecord(record, env.Defaults())
	if err := aiprovider.Apply(app, record, env.Providers); err != nil {
		return err
	}
	if err := app.Save(record); err != nil {
		// A concurrent caller can seed the singleton between our find and save;
		// the fixed ID then collides. Treat "someone else seeded it" as success.
		if existing, findErr := app.FindRecordById(CollectionName, SingletonID); findErr == nil {
			return bindProviders(app, existing)
		}
		return fmt.Errorf("seed %s: %w", CollectionName, err)
	}
	app.Logger().Info("seeded app_settings singleton from env defaults")
	return nil
}

// ApplyManaged re-applies the operator-owned settings on every boot.
//
// Only called when AI_MANAGED is on, and it writes exactly what the Settings
// page then refuses to show: the providers, the four task bindings, the
// research budget and duplicate detection. Timeouts, retries and the language
// settings are deliberately not here — they are tuning with no operator
// decision behind them, and resetting somebody's timeout on every restart of a
// container they do not control would be a bug rather than a policy.
//
// There is no digest and nothing to compare against. In managed mode the
// container's environment simply is the configuration, so the write is
// unconditional and the same on every boot.
func ApplyManaged(app core.App, env AIEnv) error {
	settings, err := app.FindRecordById(CollectionName, SingletonID)
	if err != nil {
		return fmt.Errorf("load %s: %w", CollectionName, err)
	}
	if err := aiprovider.Apply(app, settings, env.Providers); err != nil {
		return err
	}
	settings.Set("search_context_tokens", env.SearchContextTokens)
	settings.Set("near_duplicate_detection_enabled", env.NearDuplicateEnabled)
	settings.Set("near_duplicate_threshold", env.NearDuplicateThreshold)
	if err := app.Save(settings); err != nil {
		return fmt.Errorf("save %s: %w", CollectionName, err)
	}
	// The variable names are safe to log; the values are not, and one is an
	// API key.
	app.Logger().Info("applied managed AI configuration from the environment",
		"llm_sdk", env.Providers.LLM.SDK, "ocr_sdk", env.Providers.OCRSDK())
	return nil
}

func bindProviders(app core.App, record *core.Record) error {
	before := record.GetString("ocr_provider_id") + "|" + record.GetString("extract_provider_id")
	if err := aiprovider.MigrateLegacySettings(app, record); err != nil {
		return err
	}
	after := record.GetString("ocr_provider_id") + "|" + record.GetString("extract_provider_id")
	if before == after {
		return nil
	}
	return app.Save(record)
}

// Load reads runtime settings from the DB singleton. WorkerCronExpr is always from env.
func Load(app core.App) (Config, error) {
	record, err := app.FindRecordById(CollectionName, SingletonID)
	if err != nil {
		return Config{}, fmt.Errorf("load %s: %w", CollectionName, err)
	}
	return configFromRecord(app, record)
}

func FindSettingsRecord(app core.App, env AIEnv) (*core.Record, error) {
	if err := EnsureDefaults(app, env); err != nil {
		return nil, err
	}
	return app.FindRecordById(CollectionName, SingletonID)
}

func configFromRecord(app core.App, record *core.Record) (Config, error) {
	ocrTimeoutSec := int(record.GetFloat("ocr_timeout_sec"))
	if ocrTimeoutSec <= 0 {
		ocrTimeoutSec = 40
	}
	openAITimeoutSec := int(record.GetFloat("openai_timeout_sec"))
	if openAITimeoutSec <= 0 {
		openAITimeoutSec = 60
	}
	workerTimeoutSec := int(record.GetFloat("worker_timeout_sec"))
	if workerTimeoutSec <= 0 {
		workerTimeoutSec = 300
	}

	threshold := record.GetFloat("near_duplicate_threshold")
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultNearDuplicateThreshold
	}

	searchContextTokens := int(record.GetFloat("search_context_tokens"))
	if searchContextTokens <= 0 {
		searchContextTokens = DefaultSearchContextTokens
	}

	cfg := Config{
		OCRProviderID:                 strings.TrimSpace(record.GetString("ocr_provider_id")),
		OCRModel:                      strings.TrimSpace(record.GetString("ocr_model")),
		ExtractProviderID:             strings.TrimSpace(record.GetString("extract_provider_id")),
		ExtractModel:                  strings.TrimSpace(record.GetString("extract_model")),
		ChatProviderID:                strings.TrimSpace(record.GetString("chat_provider_id")),
		ChatModel:                     strings.TrimSpace(record.GetString("chat_model")),
		SearchProviderID:              strings.TrimSpace(record.GetString("search_provider_id")),
		SearchModel:                   strings.TrimSpace(record.GetString("search_model")),
		OCRTimeout:                    time.Duration(ocrTimeoutSec) * time.Second,
		ProcessingResultLanguage:      strings.ToLower(strings.TrimSpace(record.GetString("processing_result_language"))),
		DeepSearchLanguages:           NormalizeLanguageList(record.GetString("deep_search_languages")),
		SearchContextTokens:           searchContextTokens,
		OpenAITimeout:                 time.Duration(openAITimeoutSec) * time.Second,
		WorkerCronExpr:                WorkerCronFromEnv(),
		WorkerTimeout:                 time.Duration(workerTimeoutSec) * time.Second,
		WorkerMaxRetries:              max(int(record.GetFloat("worker_max_retries")), 0),
		ExtractionPromptVer:           strutil.FirstNonEmpty(record.GetString("extraction_prompt_version"), "v1"),
		NearDuplicateDetectionEnabled: record.GetBool("near_duplicate_detection_enabled"),
		NearDuplicateThreshold:        threshold,
	}

	if err := resolveProviders(app, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyBindingFallbacks fills unset chat/search bindings: chat falls back to the
// extraction binding, and search falls back to the (already resolved) chat one.
// Kept separate from the DB lookups so the fallback chain stays easy to reason
// about and test.
func applyBindingFallbacks(cfg *Config) {
	if cfg.ChatProviderID == "" {
		cfg.ChatProviderID = cfg.ExtractProviderID
	}
	if cfg.ChatModel == "" {
		cfg.ChatModel = cfg.ExtractModel
	}
	if cfg.SearchProviderID == "" {
		cfg.SearchProviderID = cfg.ChatProviderID
	}
	if cfg.SearchModel == "" {
		cfg.SearchModel = cfg.ChatModel
	}
}

func resolveProviders(app core.App, cfg *Config) error {
	applyBindingFallbacks(cfg)

	for _, binding := range []struct {
		id     string
		target **aiprovider.Provider
	}{
		{cfg.OCRProviderID, &cfg.OCRProvider},
		{cfg.ExtractProviderID, &cfg.ExtractProvider},
		{cfg.ChatProviderID, &cfg.ChatProvider},
		{cfg.SearchProviderID, &cfg.SearchProvider},
	} {
		provider, err := lookupProvider(app, binding.id)
		if err != nil {
			return err
		}
		*binding.target = provider
	}
	return nil
}

func lookupProvider(app core.App, id string) (*aiprovider.Provider, error) {
	p, err := aiprovider.FindByID(app, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup provider %s: %w", id, err)
	}
	return p, nil
}

func applyConfigToRecord(record *core.Record, cfg Config) {
	record.Set("ocr_provider_id", cfg.OCRProviderID)
	record.Set("ocr_model", cfg.OCRModel)
	record.Set("extract_provider_id", cfg.ExtractProviderID)
	record.Set("extract_model", cfg.ExtractModel)
	record.Set("chat_provider_id", cfg.ChatProviderID)
	record.Set("chat_model", cfg.ChatModel)
	record.Set("search_provider_id", cfg.SearchProviderID)
	record.Set("search_model", cfg.SearchModel)
	record.Set("ocr_timeout_sec", int(cfg.OCRTimeout.Seconds()))
	record.Set("processing_result_language", cfg.ProcessingResultLanguage)
	record.Set("deep_search_languages", cfg.DeepSearchLanguages)
	record.Set("search_context_tokens", cfg.SearchContextTokens)
	record.Set("openai_timeout_sec", int(cfg.OpenAITimeout.Seconds()))
	record.Set("worker_timeout_sec", int(cfg.WorkerTimeout.Seconds()))
	record.Set("worker_max_retries", cfg.WorkerMaxRetries)
	record.Set("extraction_prompt_version", cfg.ExtractionPromptVer)
	record.Set("near_duplicate_detection_enabled", cfg.NearDuplicateDetectionEnabled)
	threshold := cfg.NearDuplicateThreshold
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultNearDuplicateThreshold
	}
	record.Set("near_duplicate_threshold", threshold)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envIntDefault parses an int env var, falling back when unset, malformed, or
// below min — a typo like "6O" must not become a zero-second HTTP timeout.
func envIntDefault(key string, fallback, min int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < min {
		return fallback
	}
	return parsed
}

// envInt64Default parses an int64 env var, falling back when unset, malformed,
// or below min.
func envInt64Default(key string, fallback, min int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	if err != nil || parsed < min {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil || parsed <= 0 || parsed > 1 {
		return fallback
	}
	return parsed
}

// NormalizeLanguageList cleans a comma-separated ISO 639-1 list (e.g. "de, en, uk").
func NormalizeLanguageList(raw string) string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		code := strings.ToLower(strings.TrimSpace(part))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return strings.Join(out, ",")
}

func HasLLM(cfg Config) bool {
	p := cfg.ExtractProvider
	if p == nil {
		p = cfg.ChatProvider
	}
	return p != nil && p.APIKey != "" && aiprovider.IsLLM(p.SDK)
}

func HasOCR(cfg Config) bool {
	p := cfg.OCRProvider
	if p == nil || p.APIKey == "" {
		return false
	}
	if aiprovider.RequiresOCRModel(p.SDK) && strings.TrimSpace(cfg.OCRModel) == "" {
		return false
	}
	return true
}
