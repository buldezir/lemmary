package aiprovider

import (
	"os"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

func SeedFromEnv(app core.App, settings *core.Record) error {
	if settings == nil {
		return nil
	}
	if alreadyBound(settings) {
		return nil
	}
	count, err := app.CountRecords(CollectionName)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	openaiID, err := createEnvProvider(app, SDKOpenAI, "OPENAI_API_KEY", os.Getenv("OPENAI_BASE_URL"))
	if err != nil {
		return err
	}
	mistralID, err := createEnvProvider(app, SDKMistral, "MISTRAL_API_KEY", firstNonEmpty(os.Getenv("MISTRAL_API_BASE_URL"), DefaultBaseURL(SDKMistral)))
	if err != nil {
		return err
	}
	googleID, err := createEnvProvider(app, SDKGoogleVision, "GOOGLE_VISION_API_KEY", "")
	if err != nil {
		return err
	}

	bindFromIDs(settings, openaiID, mistralID, googleID, envTaskModels())
	return nil
}

func MigrateLegacySettings(app core.App, settings *core.Record) error {
	if settings == nil {
		return nil
	}
	if alreadyBound(settings) {
		return nil
	}
	count, err := app.CountRecords(CollectionName)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	openaiID, err := createLegacyProvider(app, SDKOpenAI, settings.GetString("openai_api_key"), settings.GetString("openai_base_url"), DefaultAlias(SDKOpenAI))
	if err != nil {
		return err
	}
	mistralID, err := createLegacyProvider(app, SDKMistral, settings.GetString("mistral_api_key"), settings.GetString("mistral_api_base_url"), DefaultAlias(SDKMistral))
	if err != nil {
		return err
	}
	googleID, err := createLegacyProvider(app, SDKGoogleVision, settings.GetString("google_vision_api_key"), "", DefaultAlias(SDKGoogleVision))
	if err != nil {
		return err
	}

	models := taskModels{
		extract: firstNonEmpty(settings.GetString("openai_model"), "gpt-4o-mini"),
		chat:    strings.TrimSpace(settings.GetString("openai_chat_model")),
		search:  strings.TrimSpace(settings.GetString("openai_search_model")),
		ocr:     firstNonEmpty(settings.GetString("mistral_ocr_model"), "mistral-ocr-latest"),
		ocrSDK:  firstNonEmpty(settings.GetString("ocr_provider"), "google_vision"),
	}
	bindFromIDs(settings, openaiID, mistralID, googleID, models)
	return nil
}

type taskModels struct {
	extract string
	chat    string
	search  string
	ocr     string
	ocrSDK  string
}

func envTaskModels() taskModels {
	extract := getenv("OPENAI_MODEL", "gpt-4o-mini")
	chat := getenv("OPENAI_CHAT_MODEL", extract)
	return taskModels{
		extract: extract,
		chat:    chat,
		search:  getenv("OPENAI_SEARCH_MODEL", chat),
		ocr:     getenv("MISTRAL_OCR_MODEL", "mistral-ocr-latest"),
		ocrSDK:  getenv("OCR_PROVIDER", "google_vision"),
	}
}

func bindFromIDs(settings *core.Record, openaiID, mistralID, googleID string, models taskModels) {
	switch models.ocrSDK {
	case SDKMistral:
		if mistralID != "" {
			settings.Set("ocr_provider_id", mistralID)
			settings.Set("ocr_model", models.ocr)
		}
	case SDKOpenAI, SDKOpenRouter:
		if openaiID != "" {
			settings.Set("ocr_provider_id", openaiID)
			settings.Set("ocr_model", models.extract)
		}
	default:
		if googleID != "" {
			settings.Set("ocr_provider_id", googleID)
			settings.Set("ocr_model", "")
		} else if mistralID != "" {
			settings.Set("ocr_provider_id", mistralID)
			settings.Set("ocr_model", models.ocr)
		} else if openaiID != "" {
			settings.Set("ocr_provider_id", openaiID)
			settings.Set("ocr_model", models.extract)
		}
	}

	if openaiID != "" {
		settings.Set("extract_provider_id", openaiID)
		settings.Set("extract_model", models.extract)
		chatModel := models.chat
		if chatModel == "" {
			chatModel = models.extract
		}
		settings.Set("chat_provider_id", openaiID)
		settings.Set("chat_model", chatModel)
		searchModel := models.search
		if searchModel == "" {
			searchModel = chatModel
		}
		settings.Set("search_provider_id", openaiID)
		settings.Set("search_model", searchModel)
	}
}

func alreadyBound(settings *core.Record) bool {
	return strings.TrimSpace(settings.GetString("ocr_provider_id")) != "" ||
		strings.TrimSpace(settings.GetString("extract_provider_id")) != ""
}

func createEnvProvider(app core.App, sdk, envKey, baseURL string) (string, error) {
	key := strings.TrimSpace(os.Getenv(envKey))
	if key == "" {
		return "", nil
	}
	return createLegacyProvider(app, sdk, key, baseURL, DefaultAlias(sdk))
}

func createLegacyProvider(app core.App, sdk, apiKey, baseURL, alias string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", nil
	}
	collection, err := EnsureCollection(app)
	if err != nil {
		return "", err
	}
	alias = uniqueAlias(app, alias)
	record := core.NewRecord(collection)
	record.Set("sdk", sdk)
	record.Set("alias", alias)
	record.Set("base_url", NormalizeBaseURL(sdk, baseURL))
	record.Set("api_key", apiKey)
	if err := app.Save(record); err != nil {
		return "", err
	}
	return record.Id, nil
}

func uniqueAlias(app core.App, alias string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = "Provider"
	}
	candidate := alias
	for i := 2; i < 50; i++ {
		existing, err := FindByAlias(app, candidate)
		if err != nil || existing == nil {
			return candidate
		}
		candidate = alias + " " + strconv.Itoa(i)
	}
	return alias
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
