package aiprovider

import (
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/strutil"
)

// MigrateLegacySettings builds provider records from the flat columns an
// install carried before ai_providers existed.
//
// It reads the settings record, never the environment — that separation is what
// keeps it working now that the environment no longer speaks in OPENAI_* and
// MISTRAL_* at all. An install last booted before those columns were retired
// still has them populated, and this is its only route forward.
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
		extract:    strutil.FirstNonEmpty(settings.GetString("openai_model"), DefaultExtractModel),
		chat:       strings.TrimSpace(settings.GetString("openai_chat_model")),
		search:     strings.TrimSpace(settings.GetString("openai_search_model")),
		ocr:        strutil.FirstNonEmpty(settings.GetString("mistral_ocr_model"), "mistral-ocr-latest"),
		ocrSDK:     strutil.FirstNonEmpty(settings.GetString("ocr_provider"), SDKGoogleVision),
		mistralLLM: "mistral-small-latest",
	}
	bindFromIDs(settings, openaiID, mistralID, googleID, models)
	return nil
}

type taskModels struct {
	extract    string
	chat       string
	search     string
	ocr        string
	ocrSDK     string
	mistralLLM string
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
		bindLLM(settings, openaiID, models.extract, models.chat, models.search)
	} else if mistralID != "" {
		model := strutil.FirstNonEmpty(models.mistralLLM, "mistral-small-latest")
		bindLLM(settings, mistralID, model, model, model)
	}
}

func bindLLM(settings *core.Record, providerID, extract, chat, search string) {
	settings.Set("extract_provider_id", providerID)
	settings.Set("extract_model", extract)
	chatModel := chat
	if chatModel == "" {
		chatModel = extract
	}
	settings.Set("chat_provider_id", providerID)
	settings.Set("chat_model", chatModel)
	searchModel := search
	if searchModel == "" {
		searchModel = chatModel
	}
	settings.Set("search_provider_id", providerID)
	settings.Set("search_model", searchModel)
}

func alreadyBound(settings *core.Record) bool {
	return strings.TrimSpace(settings.GetString("ocr_provider_id")) != "" ||
		strings.TrimSpace(settings.GetString("extract_provider_id")) != ""
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
