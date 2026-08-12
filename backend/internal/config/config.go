package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/aiprovider"
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
	OpenAITimeout                 time.Duration
	WorkerCronExpr                string
	WorkerTimeout                 time.Duration
	WorkerMaxRetries              int
	ExtractionPromptVer           string
	NearDuplicateDetectionEnabled bool
	NearDuplicateThreshold        float64
}

// DefaultsFromEnv builds a Config from environment variables (and code defaults).
// Used to seed the DB singleton on first boot and as an in-memory fallback.
// WorkerCronExpr always comes from env.
func DefaultsFromEnv() Config {
	timeoutSec, _ := strconv.Atoi(getEnv("OPENAI_TIMEOUT_SEC", "60"))
	ocrTimeoutSec, _ := strconv.Atoi(getEnv("OCR_TIMEOUT_SEC", "40"))
	workerTimeoutSec, _ := strconv.Atoi(getEnv("WORKER_TIMEOUT_SEC", "300"))
	maxRetries, _ := strconv.Atoi(getEnv("WORKER_MAX_RETRIES", "0"))

	openAIModel := getEnv("OPENAI_MODEL", "gpt-4o-mini")
	chatModel := getEnv("OPENAI_CHAT_MODEL", openAIModel)

	return Config{
		OCRModel:                      getEnv("MISTRAL_OCR_MODEL", "mistral-ocr-latest"),
		ExtractModel:                  openAIModel,
		ChatModel:                     chatModel,
		SearchModel:                   getEnv("OPENAI_SEARCH_MODEL", chatModel),
		OCRTimeout:                    time.Duration(ocrTimeoutSec) * time.Second,
		ProcessingResultLanguage:      strings.ToLower(strings.TrimSpace(os.Getenv("PROCESSING_RESULT_LANGUAGE"))),
		DeepSearchLanguages:           normalizeLanguageList(os.Getenv("DEEP_SEARCH_LANGUAGES")),
		OpenAITimeout:                 time.Duration(timeoutSec) * time.Second,
		WorkerCronExpr:                WorkerCronFromEnv(),
		WorkerTimeout:                 time.Duration(workerTimeoutSec) * time.Second,
		WorkerMaxRetries:              maxRetries,
		ExtractionPromptVer:           getEnv("EXTRACTION_PROMPT_VERSION", "v1"),
		NearDuplicateDetectionEnabled: getEnvBool("NEAR_DUPLICATE_DETECTION_ENABLED", false),
		NearDuplicateThreshold:        getEnvFloat("NEAR_DUPLICATE_THRESHOLD", DefaultNearDuplicateThreshold),
	}
}

const DefaultNearDuplicateThreshold = 0.92

func WorkerCronFromEnv() string {
	return getEnv("WORKER_CRON_EXPR", "* * * * *")
}

func bindingFields() []core.Field {
	return []core.Field{
		&core.TextField{Name: "ocr_provider_id", Max: 15},
		&core.TextField{Name: "ocr_model", Max: 200},
		&core.TextField{Name: "extract_provider_id", Max: 15},
		&core.TextField{Name: "extract_model", Max: 200},
		&core.TextField{Name: "chat_provider_id", Max: 15},
		&core.TextField{Name: "chat_model", Max: 200},
		&core.TextField{Name: "search_provider_id", Max: 15},
		&core.TextField{Name: "search_model", Max: 200},
	}
}

// EnsureCollection creates the app_settings collection if it does not exist yet.
func EnsureCollection(app core.App) (*core.Collection, error) {
	if collection, err := app.FindCollectionByNameOrId(CollectionName); err == nil {
		return collection, nil
	}

	settings := core.NewBaseCollection(CollectionName)
	// Locked down for regular users; superusers bypass rules.
	settings.Fields.Add(
		&core.TextField{Name: "ocr_provider", Max: 50},
		&core.TextField{Name: "google_vision_api_key", Max: 2000},
		&core.TextField{Name: "mistral_api_key", Max: 2000},
		&core.TextField{Name: "mistral_ocr_model", Max: 200},
		&core.TextField{Name: "mistral_api_base_url", Max: 500},
		&core.NumberField{Name: "ocr_timeout_sec", OnlyInt: true},
		&core.TextField{Name: "processing_result_language", Max: 16},
		&core.TextField{Name: "deep_search_languages", Max: 200},
		&core.TextField{Name: "openai_api_key", Max: 2000},
		&core.TextField{Name: "openai_model", Max: 200},
		&core.TextField{Name: "openai_chat_model", Max: 200},
		&core.TextField{Name: "openai_search_model", Max: 200},
		&core.TextField{Name: "openai_base_url", Max: 500},
		&core.NumberField{Name: "openai_timeout_sec", OnlyInt: true},
		&core.NumberField{Name: "worker_timeout_sec", OnlyInt: true},
		&core.NumberField{Name: "worker_max_retries", OnlyInt: true},
		&core.TextField{Name: "extraction_prompt_version", Max: 50},
		&core.BoolField{Name: "near_duplicate_detection_enabled"},
		&core.NumberField{Name: "near_duplicate_threshold"},
	)
	for _, field := range bindingFields() {
		settings.Fields.Add(field)
	}
	settings.Fields.Add(
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	if err := app.Save(settings); err != nil {
		return nil, fmt.Errorf("create %s collection: %w", CollectionName, err)
	}
	return settings, nil
}

// EnsureCollectionFields adds any missing app_settings fields (for upgrades).
func EnsureCollectionFields(app core.App) error {
	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return nil
	}
	changed := false
	if collection.Fields.GetByName("deep_search_languages") == nil {
		collection.Fields.Add(&core.TextField{Name: "deep_search_languages", Max: 200})
		changed = true
	}
	if collection.Fields.GetByName("openai_search_model") == nil {
		collection.Fields.Add(&core.TextField{Name: "openai_search_model", Max: 200})
		changed = true
	}
	if collection.Fields.GetByName("near_duplicate_detection_enabled") == nil {
		collection.Fields.Add(&core.BoolField{Name: "near_duplicate_detection_enabled"})
		changed = true
	}
	if collection.Fields.GetByName("near_duplicate_threshold") == nil {
		collection.Fields.Add(&core.NumberField{Name: "near_duplicate_threshold"})
		changed = true
	}
	for _, field := range bindingFields() {
		if collection.Fields.GetByName(field.GetName()) == nil {
			collection.Fields.Add(field)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return app.Save(collection)
}

// EnsureDefaults creates the app_settings collection + singleton from env if missing.
func EnsureDefaults(app core.App) error {
	if _, err := aiprovider.EnsureCollection(app); err != nil {
		return err
	}
	if err := EnsureCollectionFields(app); err != nil {
		return err
	}

	if record, err := app.FindRecordById(CollectionName, SingletonID); err == nil {
		return bindProviders(app, record)
	}

	collection, err := EnsureCollection(app)
	if err != nil {
		return err
	}

	// Re-check after ensuring collection (race / concurrent bootstrap).
	if record, err := app.FindRecordById(CollectionName, SingletonID); err == nil {
		return bindProviders(app, record)
	}

	cfg := DefaultsFromEnv()
	record := core.NewRecord(collection)
	record.Id = SingletonID
	record.MarkAsNew()
	applyConfigToRecord(record, cfg)
	// Keep legacy columns populated so upgrades/migrations can copy them.
	record.Set("ocr_provider", getEnv("OCR_PROVIDER", "google_vision"))
	record.Set("google_vision_api_key", os.Getenv("GOOGLE_VISION_API_KEY"))
	record.Set("mistral_api_key", os.Getenv("MISTRAL_API_KEY"))
	record.Set("mistral_ocr_model", cfg.OCRModel)
	record.Set("mistral_api_base_url", getEnv("MISTRAL_API_BASE_URL", aiprovider.DefaultBaseURL(aiprovider.SDKMistral)))
	record.Set("openai_api_key", os.Getenv("OPENAI_API_KEY"))
	record.Set("openai_model", cfg.ExtractModel)
	record.Set("openai_chat_model", cfg.ChatModel)
	record.Set("openai_search_model", cfg.SearchModel)
	record.Set("openai_base_url", getEnv("OPENAI_BASE_URL", aiprovider.DefaultBaseURL(aiprovider.SDKOpenAI)))
	if err := aiprovider.SeedFromEnv(app, record); err != nil {
		return err
	}
	if err := app.Save(record); err != nil {
		return fmt.Errorf("seed %s: %w", CollectionName, err)
	}
	app.Logger().Info("seeded app_settings singleton from env defaults")
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

func FindSettingsRecord(app core.App) (*core.Record, error) {
	if err := EnsureDefaults(app); err != nil {
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
		DeepSearchLanguages:           normalizeLanguageList(record.GetString("deep_search_languages")),
		OpenAITimeout:                 time.Duration(openAITimeoutSec) * time.Second,
		WorkerCronExpr:                WorkerCronFromEnv(),
		WorkerTimeout:                 time.Duration(workerTimeoutSec) * time.Second,
		WorkerMaxRetries:              int(record.GetFloat("worker_max_retries")),
		ExtractionPromptVer:           firstNonEmpty(record.GetString("extraction_prompt_version"), "v1"),
		NearDuplicateDetectionEnabled: record.GetBool("near_duplicate_detection_enabled"),
		NearDuplicateThreshold:        threshold,
	}

	if err := resolveProviders(app, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func resolveProviders(app core.App, cfg *Config) error {
	ocr, err := lookupProvider(app, cfg.OCRProviderID)
	if err != nil {
		return err
	}
	extract, err := lookupProvider(app, cfg.ExtractProviderID)
	if err != nil {
		return err
	}

	chatID := cfg.ChatProviderID
	chatModel := cfg.ChatModel
	if chatID == "" {
		chatID = cfg.ExtractProviderID
	}
	if chatModel == "" {
		chatModel = cfg.ExtractModel
	}
	chat, err := lookupProvider(app, chatID)
	if err != nil {
		return err
	}

	searchID := cfg.SearchProviderID
	searchModel := cfg.SearchModel
	if searchID == "" {
		searchID = chatID
	}
	if searchModel == "" {
		searchModel = chatModel
	}
	search, err := lookupProvider(app, searchID)
	if err != nil {
		return err
	}

	cfg.OCRProvider = ocr
	cfg.ExtractProvider = extract
	cfg.ChatProvider = chat
	cfg.SearchProvider = search
	cfg.ChatProviderID = chatID
	cfg.ChatModel = chatModel
	cfg.SearchProviderID = searchID
	cfg.SearchModel = searchModel
	return nil
}

func lookupProvider(app core.App, id string) (*aiprovider.Provider, error) {
	p, err := aiprovider.FindByID(app, id)
	if err != nil {
		if strings.TrimSpace(id) == "" {
			return nil, nil
		}
		// Missing record is a soft miss so the process stays up.
		return nil, nil
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// normalizeLanguageList cleans a comma-separated ISO 639-1 list (e.g. "de, en, uk").
func normalizeLanguageList(raw string) string {
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
