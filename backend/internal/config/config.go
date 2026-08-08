package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const (
	CollectionName = "app_settings"
	SingletonID    = "appsettings0001" // must be 15 chars (PocketBase default id rules)
)

type Config struct {
	OCRProvider                     string
	GoogleVisionAPIKey              string
	MistralAPIKey                   string
	MistralOCRModel                 string
	MistralAPIBaseURL               string
	OCRTimeout                      time.Duration
	ProcessingResultLanguage        string
	DeepSearchLanguages             string
	OpenAIAPIKey                    string
	OpenAIModel                     string
	OpenAIChatModel                 string
	OpenAISearchModel               string
	OpenAIBaseURL                   string
	OpenAITimeout                   time.Duration
	WorkerCronExpr                  string
	WorkerTimeout                   time.Duration
	WorkerMaxRetries                int
	ExtractionPromptVer             string
	NearDuplicateDetectionEnabled   bool
	NearDuplicateThreshold          float64
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
		OCRProvider:                   getEnv("OCR_PROVIDER", "google_vision"),
		GoogleVisionAPIKey:            os.Getenv("GOOGLE_VISION_API_KEY"),
		MistralAPIKey:                 os.Getenv("MISTRAL_API_KEY"),
		MistralOCRModel:               getEnv("MISTRAL_OCR_MODEL", "mistral-ocr-latest"),
		MistralAPIBaseURL:             getEnv("MISTRAL_API_BASE_URL", "https://api.mistral.ai/v1"),
		OCRTimeout:                    time.Duration(ocrTimeoutSec) * time.Second,
		ProcessingResultLanguage:      strings.ToLower(strings.TrimSpace(os.Getenv("PROCESSING_RESULT_LANGUAGE"))),
		DeepSearchLanguages:           normalizeLanguageList(os.Getenv("DEEP_SEARCH_LANGUAGES")),
		OpenAIAPIKey:                  os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:                   openAIModel,
		OpenAIChatModel:               chatModel,
		OpenAISearchModel:             getEnv("OPENAI_SEARCH_MODEL", chatModel),
		OpenAIBaseURL:                 getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
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
	if !changed {
		return nil
	}
	return app.Save(collection)
}

// EnsureDefaults creates the app_settings collection + singleton from env if missing.
func EnsureDefaults(app core.App) error {
	if err := EnsureCollectionFields(app); err != nil {
		return err
	}

	if _, err := app.FindRecordById(CollectionName, SingletonID); err == nil {
		return nil
	}

	collection, err := EnsureCollection(app)
	if err != nil {
		return err
	}

	// Re-check after ensuring collection (race / concurrent bootstrap).
	if _, err := app.FindRecordById(CollectionName, SingletonID); err == nil {
		return nil
	}

	cfg := DefaultsFromEnv()
	record := core.NewRecord(collection)
	record.Id = SingletonID
	record.MarkAsNew()
	applyConfigToRecord(record, cfg)
	if err := app.Save(record); err != nil {
		return fmt.Errorf("seed %s: %w", CollectionName, err)
	}
	app.Logger().Info("seeded app_settings singleton from env defaults")
	return nil
}

// Load reads runtime settings from the DB singleton. WorkerCronExpr is always from env.
func Load(app core.App) (Config, error) {
	record, err := app.FindRecordById(CollectionName, SingletonID)
	if err != nil {
		return Config{}, fmt.Errorf("load %s: %w", CollectionName, err)
	}
	return configFromRecord(record), nil
}

func FindSettingsRecord(app core.App) (*core.Record, error) {
	if err := EnsureDefaults(app); err != nil {
		return nil, err
	}
	return app.FindRecordById(CollectionName, SingletonID)
}

func configFromRecord(record *core.Record) Config {
	openAIModel := strings.TrimSpace(record.GetString("openai_model"))
	if openAIModel == "" {
		openAIModel = "gpt-4o-mini"
	}
	chatModel := strings.TrimSpace(record.GetString("openai_chat_model"))
	if chatModel == "" {
		chatModel = openAIModel
	}
	searchModel := strings.TrimSpace(record.GetString("openai_search_model"))
	if searchModel == "" {
		searchModel = chatModel
	}

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

	ocrProvider := strings.TrimSpace(record.GetString("ocr_provider"))
	if ocrProvider == "" {
		ocrProvider = "google_vision"
	}

	threshold := record.GetFloat("near_duplicate_threshold")
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultNearDuplicateThreshold
	}

	return Config{
		OCRProvider:                   ocrProvider,
		GoogleVisionAPIKey:            record.GetString("google_vision_api_key"),
		MistralAPIKey:                 record.GetString("mistral_api_key"),
		MistralOCRModel:               firstNonEmpty(record.GetString("mistral_ocr_model"), "mistral-ocr-latest"),
		MistralAPIBaseURL:             firstNonEmpty(record.GetString("mistral_api_base_url"), "https://api.mistral.ai/v1"),
		OCRTimeout:                    time.Duration(ocrTimeoutSec) * time.Second,
		ProcessingResultLanguage:      strings.ToLower(strings.TrimSpace(record.GetString("processing_result_language"))),
		DeepSearchLanguages:           normalizeLanguageList(record.GetString("deep_search_languages")),
		OpenAIAPIKey:                  record.GetString("openai_api_key"),
		OpenAIModel:                   openAIModel,
		OpenAIChatModel:               chatModel,
		OpenAISearchModel:             searchModel,
		OpenAIBaseURL:                 firstNonEmpty(record.GetString("openai_base_url"), "https://api.openai.com/v1"),
		OpenAITimeout:                 time.Duration(openAITimeoutSec) * time.Second,
		WorkerCronExpr:                WorkerCronFromEnv(),
		WorkerTimeout:                 time.Duration(workerTimeoutSec) * time.Second,
		WorkerMaxRetries:              int(record.GetFloat("worker_max_retries")),
		ExtractionPromptVer:           firstNonEmpty(record.GetString("extraction_prompt_version"), "v1"),
		NearDuplicateDetectionEnabled: record.GetBool("near_duplicate_detection_enabled"),
		NearDuplicateThreshold:        threshold,
	}
}

func applyConfigToRecord(record *core.Record, cfg Config) {
	record.Set("ocr_provider", cfg.OCRProvider)
	record.Set("google_vision_api_key", cfg.GoogleVisionAPIKey)
	record.Set("mistral_api_key", cfg.MistralAPIKey)
	record.Set("mistral_ocr_model", cfg.MistralOCRModel)
	record.Set("mistral_api_base_url", cfg.MistralAPIBaseURL)
	record.Set("ocr_timeout_sec", int(cfg.OCRTimeout.Seconds()))
	record.Set("processing_result_language", cfg.ProcessingResultLanguage)
	record.Set("deep_search_languages", cfg.DeepSearchLanguages)
	record.Set("openai_api_key", cfg.OpenAIAPIKey)
	record.Set("openai_model", cfg.OpenAIModel)
	record.Set("openai_chat_model", cfg.OpenAIChatModel)
	record.Set("openai_search_model", cfg.OpenAISearchModel)
	record.Set("openai_base_url", cfg.OpenAIBaseURL)
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
