package appapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

type settingsResponse struct {
	OCRProviderID                 string  `json:"ocr_provider_id"`
	OCRModel                      string  `json:"ocr_model"`
	ExtractProviderID             string  `json:"extract_provider_id"`
	ExtractModel                  string  `json:"extract_model"`
	ChatProviderID                string  `json:"chat_provider_id"`
	ChatModel                     string  `json:"chat_model"`
	SearchProviderID              string  `json:"search_provider_id"`
	SearchModel                   string  `json:"search_model"`
	OCRTimeoutSec                 int     `json:"ocr_timeout_sec"`
	ProcessingResultLanguage      string  `json:"processing_result_language"`
	DeepSearchLanguages           string  `json:"deep_search_languages"`
	SearchContextTokens           int     `json:"search_context_tokens"`
	OpenAITimeoutSec              int     `json:"openai_timeout_sec"`
	WorkerTimeoutSec              int     `json:"worker_timeout_sec"`
	WorkerMaxRetries              int     `json:"worker_max_retries"`
	ExtractionPromptVersion       string  `json:"extraction_prompt_version"`
	NearDuplicateDetectionEnabled bool    `json:"near_duplicate_detection_enabled"`
	NearDuplicateThreshold        float64 `json:"near_duplicate_threshold"`
}

type settingsPatchRequest struct {
	OCRProviderID                 *string  `json:"ocr_provider_id"`
	OCRModel                      *string  `json:"ocr_model"`
	ExtractProviderID             *string  `json:"extract_provider_id"`
	ExtractModel                  *string  `json:"extract_model"`
	ChatProviderID                *string  `json:"chat_provider_id"`
	ChatModel                     *string  `json:"chat_model"`
	SearchProviderID              *string  `json:"search_provider_id"`
	SearchModel                   *string  `json:"search_model"`
	OCRTimeoutSec                 *int     `json:"ocr_timeout_sec"`
	ProcessingResultLanguage      *string  `json:"processing_result_language"`
	DeepSearchLanguages           *string  `json:"deep_search_languages"`
	SearchContextTokens           *int     `json:"search_context_tokens"`
	OpenAITimeoutSec              *int     `json:"openai_timeout_sec"`
	WorkerTimeoutSec              *int     `json:"worker_timeout_sec"`
	WorkerMaxRetries              *int     `json:"worker_max_retries"`
	ExtractionPromptVersion       *string  `json:"extraction_prompt_version"`
	NearDuplicateDetectionEnabled *bool    `json:"near_duplicate_detection_enabled"`
	NearDuplicateThreshold        *float64 `json:"near_duplicate_threshold"`
}

func handleGetSettings(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if err := config.EnsureDefaults(app); err != nil {
			app.Logger().Warn("ensure settings before GET failed", "error", err)
		}
		// No reload here: the runtime is rebuilt by the app_settings/ai_providers
		// record hooks, so reads stay cheap and quiet.
		return writeJSON(e, http.StatusOK, settingsResponseFromConfig(rt.Snapshot().Cfg))
	}
}

func handlePatchSettings(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req settingsPatchRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}

		// Load + patch + save in one transaction: settings is a singleton record
		// saved whole, so two concurrent PATCHes would otherwise silently revert
		// each other's fields.
		var patchErr error
		err := app.RunInTransaction(func(txApp core.App) error {
			record, err := config.FindSettingsRecord(txApp)
			if err != nil {
				return err
			}
			if err := applySettingsPatch(txApp, record, req); err != nil {
				patchErr = err
				return err
			}
			return txApp.Save(record)
		})
		if err != nil {
			if patchErr != nil {
				return writeError(e, http.StatusBadRequest, patchErr.Error())
			}
			app.Logger().Error("save settings failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to save settings.")
		}

		// app.Save above fires OnRecordAfterUpdateSuccess, which reloads the runtime.
		return writeJSON(e, http.StatusOK, settingsResponseFromConfig(rt.Snapshot().Cfg))
	}
}

func settingsResponseFromConfig(cfg config.Config) settingsResponse {
	threshold := cfg.NearDuplicateThreshold
	if threshold <= 0 || threshold > 1 {
		threshold = config.DefaultNearDuplicateThreshold
	}
	return settingsResponse{
		OCRProviderID:                 cfg.OCRProviderID,
		OCRModel:                      cfg.OCRModel,
		ExtractProviderID:             cfg.ExtractProviderID,
		ExtractModel:                  cfg.ExtractModel,
		ChatProviderID:                cfg.ChatProviderID,
		ChatModel:                     cfg.ChatModel,
		SearchProviderID:              cfg.SearchProviderID,
		SearchModel:                   cfg.SearchModel,
		OCRTimeoutSec:                 int(cfg.OCRTimeout.Seconds()),
		ProcessingResultLanguage:      cfg.ProcessingResultLanguage,
		DeepSearchLanguages:           cfg.DeepSearchLanguages,
		SearchContextTokens:           cfg.SearchContextTokens,
		OpenAITimeoutSec:              int(cfg.OpenAITimeout.Seconds()),
		WorkerTimeoutSec:              int(cfg.WorkerTimeout.Seconds()),
		WorkerMaxRetries:              cfg.WorkerMaxRetries,
		ExtractionPromptVersion:       cfg.ExtractionPromptVer,
		NearDuplicateDetectionEnabled: cfg.NearDuplicateDetectionEnabled,
		NearDuplicateThreshold:        threshold,
	}
}

func applySettingsPatch(app core.App, record *core.Record, req settingsPatchRequest) error {
	if req.OCRProviderID != nil {
		id := strings.TrimSpace(*req.OCRProviderID)
		if err := validateProviderID(app, id, false); err != nil {
			return err
		}
		record.Set("ocr_provider_id", id)
	}
	if req.OCRModel != nil {
		record.Set("ocr_model", strings.TrimSpace(*req.OCRModel))
	}
	if req.ExtractProviderID != nil {
		id := strings.TrimSpace(*req.ExtractProviderID)
		if err := validateProviderID(app, id, true); err != nil {
			return err
		}
		record.Set("extract_provider_id", id)
	}
	if req.ExtractModel != nil {
		record.Set("extract_model", strings.TrimSpace(*req.ExtractModel))
	}
	if req.ChatProviderID != nil {
		id := strings.TrimSpace(*req.ChatProviderID)
		if err := validateProviderID(app, id, true); err != nil {
			return err
		}
		record.Set("chat_provider_id", id)
	}
	if req.ChatModel != nil {
		record.Set("chat_model", strings.TrimSpace(*req.ChatModel))
	}
	if req.SearchProviderID != nil {
		id := strings.TrimSpace(*req.SearchProviderID)
		if err := validateProviderID(app, id, true); err != nil {
			return err
		}
		record.Set("search_provider_id", id)
	}
	if req.SearchModel != nil {
		record.Set("search_model", strings.TrimSpace(*req.SearchModel))
	}
	if req.OCRTimeoutSec != nil {
		if *req.OCRTimeoutSec <= 0 {
			return errInvalid("ocr_timeout_sec must be positive")
		}
		record.Set("ocr_timeout_sec", *req.OCRTimeoutSec)
	}
	if req.ProcessingResultLanguage != nil {
		record.Set("processing_result_language", strings.ToLower(strings.TrimSpace(*req.ProcessingResultLanguage)))
	}
	if req.DeepSearchLanguages != nil {
		record.Set("deep_search_languages", config.NormalizeLanguageList(*req.DeepSearchLanguages))
	}
	if req.SearchContextTokens != nil {
		if *req.SearchContextTokens <= 0 {
			return errInvalid("search_context_tokens must be positive")
		}
		record.Set("search_context_tokens", *req.SearchContextTokens)
	}
	if req.OpenAITimeoutSec != nil {
		if *req.OpenAITimeoutSec <= 0 {
			return errInvalid("openai_timeout_sec must be positive")
		}
		record.Set("openai_timeout_sec", *req.OpenAITimeoutSec)
	}
	if req.WorkerTimeoutSec != nil {
		if *req.WorkerTimeoutSec <= 0 {
			return errInvalid("worker_timeout_sec must be positive")
		}
		record.Set("worker_timeout_sec", *req.WorkerTimeoutSec)
	}
	if req.WorkerMaxRetries != nil {
		if *req.WorkerMaxRetries < 0 {
			return errInvalid("worker_max_retries must be >= 0")
		}
		record.Set("worker_max_retries", *req.WorkerMaxRetries)
	}
	if req.ExtractionPromptVersion != nil {
		record.Set("extraction_prompt_version", strings.TrimSpace(*req.ExtractionPromptVersion))
	}
	if req.NearDuplicateDetectionEnabled != nil {
		record.Set("near_duplicate_detection_enabled", *req.NearDuplicateDetectionEnabled)
	}
	if req.NearDuplicateThreshold != nil {
		if *req.NearDuplicateThreshold <= 0 || *req.NearDuplicateThreshold > 1 {
			return errInvalid("near_duplicate_threshold must be between 0 and 1")
		}
		record.Set("near_duplicate_threshold", *req.NearDuplicateThreshold)
	}

	ocrID := strings.TrimSpace(record.GetString("ocr_provider_id"))
	if ocrID != "" {
		p, err := aiprovider.FindByID(app, ocrID)
		if err != nil || p == nil {
			return errInvalid("ocr_provider_id is not a valid provider")
		}
		if aiprovider.RequiresOCRModel(p.SDK) && strings.TrimSpace(record.GetString("ocr_model")) == "" {
			return errInvalid("ocr_model is required for this OCR provider")
		}
	}
	if extractID := strings.TrimSpace(record.GetString("extract_provider_id")); extractID != "" {
		if strings.TrimSpace(record.GetString("extract_model")) == "" {
			return errInvalid("extract_model is required")
		}
	}
	return nil
}

func validateProviderID(app core.App, id string, llmOnly bool) error {
	if id == "" {
		return nil
	}
	p, err := aiprovider.FindByID(app, id)
	if err != nil || p == nil {
		return errInvalid("unknown provider")
	}
	if llmOnly && !aiprovider.IsLLM(p.SDK) {
		return errInvalid("extraction, chat, and search require an openai, openrouter, or mistral provider")
	}
	return nil
}

type settingsError string

func (e settingsError) Error() string { return string(e) }

func errInvalid(msg string) error { return settingsError(msg) }
