package appapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/config"
)

type settingsResponse struct {
	OCRProvider                   string  `json:"ocr_provider"`
	GoogleVisionAPIKeySet         bool    `json:"google_vision_api_key_set"`
	MistralAPIKeySet              bool    `json:"mistral_api_key_set"`
	MistralOCRModel               string  `json:"mistral_ocr_model"`
	MistralAPIBaseURL             string  `json:"mistral_api_base_url"`
	OCRTimeoutSec                 int     `json:"ocr_timeout_sec"`
	ProcessingResultLanguage      string  `json:"processing_result_language"`
	DeepSearchLanguages           string  `json:"deep_search_languages"`
	OpenAIAPIKeySet               bool    `json:"openai_api_key_set"`
	OpenAIModel                   string  `json:"openai_model"`
	OpenAIChatModel               string  `json:"openai_chat_model"`
	OpenAISearchModel             string  `json:"openai_search_model"`
	OpenAIBaseURL                 string  `json:"openai_base_url"`
	OpenAITimeoutSec              int     `json:"openai_timeout_sec"`
	WorkerTimeoutSec              int     `json:"worker_timeout_sec"`
	WorkerMaxRetries              int     `json:"worker_max_retries"`
	ExtractionPromptVersion       string  `json:"extraction_prompt_version"`
	NearDuplicateDetectionEnabled bool    `json:"near_duplicate_detection_enabled"`
	NearDuplicateThreshold        float64 `json:"near_duplicate_threshold"`
}

type settingsPatchRequest struct {
	OCRProvider                   *string  `json:"ocr_provider"`
	GoogleVisionAPIKey            *string  `json:"google_vision_api_key"`
	MistralAPIKey                 *string  `json:"mistral_api_key"`
	MistralOCRModel               *string  `json:"mistral_ocr_model"`
	MistralAPIBaseURL             *string  `json:"mistral_api_base_url"`
	OCRTimeoutSec                 *int     `json:"ocr_timeout_sec"`
	ProcessingResultLanguage      *string  `json:"processing_result_language"`
	DeepSearchLanguages           *string  `json:"deep_search_languages"`
	OpenAIAPIKey                  *string  `json:"openai_api_key"`
	OpenAIModel                   *string  `json:"openai_model"`
	OpenAIChatModel               *string  `json:"openai_chat_model"`
	OpenAISearchModel             *string  `json:"openai_search_model"`
	OpenAIBaseURL                 *string  `json:"openai_base_url"`
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
			// Still return in-memory / env snapshot so the Settings form can load.
			app.Logger().Warn("ensure settings before GET failed", "error", err)
		} else {
			_ = rt.Reload(app)
		}
		return writeJSON(e, http.StatusOK, settingsResponseFromConfig(rt.Snapshot().Cfg))
	}
}

func handlePatchSettings(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req settingsPatchRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}

		record, err := config.FindSettingsRecord(app)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Settings are unavailable: "+err.Error())
		}

		if err := applySettingsPatch(record, req); err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to save settings.")
		}

		// Record hook also reloads; call explicitly so the response reflects new state
		// even if the hook is skipped for any reason.
		if err := rt.Reload(app); err != nil {
			return writeError(e, http.StatusInternalServerError, "Settings saved but reload failed.")
		}

		return writeJSON(e, http.StatusOK, settingsResponseFromConfig(rt.Snapshot().Cfg))
	}
}

func settingsResponseFromConfig(cfg config.Config) settingsResponse {
	threshold := cfg.NearDuplicateThreshold
	if threshold <= 0 || threshold > 1 {
		threshold = config.DefaultNearDuplicateThreshold
	}
	return settingsResponse{
		OCRProvider:                   cfg.OCRProvider,
		GoogleVisionAPIKeySet:         cfg.GoogleVisionAPIKey != "",
		MistralAPIKeySet:              cfg.MistralAPIKey != "",
		MistralOCRModel:               cfg.MistralOCRModel,
		MistralAPIBaseURL:             cfg.MistralAPIBaseURL,
		OCRTimeoutSec:                 int(cfg.OCRTimeout.Seconds()),
		ProcessingResultLanguage:      cfg.ProcessingResultLanguage,
		DeepSearchLanguages:           cfg.DeepSearchLanguages,
		OpenAIAPIKeySet:               cfg.OpenAIAPIKey != "",
		OpenAIModel:                   cfg.OpenAIModel,
		OpenAIChatModel:               cfg.OpenAIChatModel,
		OpenAISearchModel:             cfg.OpenAISearchModel,
		OpenAIBaseURL:                 cfg.OpenAIBaseURL,
		OpenAITimeoutSec:              int(cfg.OpenAITimeout.Seconds()),
		WorkerTimeoutSec:              int(cfg.WorkerTimeout.Seconds()),
		WorkerMaxRetries:              cfg.WorkerMaxRetries,
		ExtractionPromptVersion:       cfg.ExtractionPromptVer,
		NearDuplicateDetectionEnabled: cfg.NearDuplicateDetectionEnabled,
		NearDuplicateThreshold:        threshold,
	}
}

func applySettingsPatch(record *core.Record, req settingsPatchRequest) error {
	if req.OCRProvider != nil {
		provider := strings.TrimSpace(*req.OCRProvider)
		if provider != "google_vision" && provider != "mistral" {
			return errInvalid("ocr_provider must be google_vision or mistral")
		}
		record.Set("ocr_provider", provider)
	}
	if req.GoogleVisionAPIKey != nil && strings.TrimSpace(*req.GoogleVisionAPIKey) != "" {
		record.Set("google_vision_api_key", strings.TrimSpace(*req.GoogleVisionAPIKey))
	}
	if req.MistralAPIKey != nil && strings.TrimSpace(*req.MistralAPIKey) != "" {
		record.Set("mistral_api_key", strings.TrimSpace(*req.MistralAPIKey))
	}
	if req.MistralOCRModel != nil {
		record.Set("mistral_ocr_model", strings.TrimSpace(*req.MistralOCRModel))
	}
	if req.MistralAPIBaseURL != nil {
		record.Set("mistral_api_base_url", strings.TrimSpace(*req.MistralAPIBaseURL))
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
		record.Set("deep_search_languages", normalizeDeepSearchLanguages(*req.DeepSearchLanguages))
	}
	if req.OpenAIAPIKey != nil && strings.TrimSpace(*req.OpenAIAPIKey) != "" {
		record.Set("openai_api_key", strings.TrimSpace(*req.OpenAIAPIKey))
	}
	if req.OpenAIModel != nil {
		record.Set("openai_model", strings.TrimSpace(*req.OpenAIModel))
	}
	if req.OpenAIChatModel != nil {
		record.Set("openai_chat_model", strings.TrimSpace(*req.OpenAIChatModel))
	}
	if req.OpenAISearchModel != nil {
		record.Set("openai_search_model", strings.TrimSpace(*req.OpenAISearchModel))
	}
	if req.OpenAIBaseURL != nil {
		record.Set("openai_base_url", strings.TrimSpace(*req.OpenAIBaseURL))
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
	return nil
}

type settingsError string

func (e settingsError) Error() string { return string(e) }

func errInvalid(msg string) error { return settingsError(msg) }

func normalizeDeepSearchLanguages(raw string) string {
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
