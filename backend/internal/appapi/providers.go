package appapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/aiprovider"
	"paperless-go/backend/internal/config"
)

type providerResponse struct {
	ID        string `json:"id"`
	SDK       string `json:"sdk"`
	Alias     string `json:"alias"`
	BaseURL   string `json:"base_url"`
	APIKeySet bool   `json:"api_key_set"`
}

type providerWriteRequest struct {
	SDK     *string `json:"sdk"`
	Alias   *string `json:"alias"`
	BaseURL *string `json:"base_url"`
	APIKey  *string `json:"api_key"`
}

func providerJSON(p aiprovider.Provider) providerResponse {
	return providerResponse{
		ID:        p.ID,
		SDK:       p.SDK,
		Alias:     p.Alias,
		BaseURL:   p.BaseURL,
		APIKeySet: p.APIKey != "",
	}
}

func handleListProviders(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if _, err := aiprovider.EnsureCollection(app); err != nil {
			return writeError(e, http.StatusInternalServerError, "Providers are unavailable.")
		}
		providers, err := aiprovider.List(app)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to list providers.")
		}
		out := make([]providerResponse, 0, len(providers))
		for _, p := range providers {
			out = append(out, providerJSON(p))
		}
		return writeJSON(e, http.StatusOK, map[string]any{"providers": out})
	}
}

func handleCreateProvider(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req providerWriteRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		sdk := ""
		if req.SDK != nil {
			sdk = strings.TrimSpace(*req.SDK)
		}
		if !aiprovider.ValidSDK(sdk) {
			return writeError(e, http.StatusBadRequest, "sdk must be openai, openrouter, google_vision, or mistral.")
		}
		alias := ""
		if req.Alias != nil {
			alias = strings.TrimSpace(*req.Alias)
		}
		if alias == "" {
			alias = aiprovider.DefaultAlias(sdk)
		}
		if err := aiprovider.EnsureUniqueAlias(app, alias, ""); err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		apiKey := ""
		if req.APIKey != nil {
			apiKey = strings.TrimSpace(*req.APIKey)
		}
		if apiKey == "" {
			return writeError(e, http.StatusBadRequest, "api_key is required.")
		}
		baseURL := ""
		if req.BaseURL != nil {
			baseURL = strings.TrimSpace(*req.BaseURL)
		}

		collection, err := aiprovider.EnsureCollection(app)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Providers are unavailable.")
		}
		record := core.NewRecord(collection)
		record.Set("sdk", sdk)
		record.Set("alias", alias)
		record.Set("base_url", aiprovider.NormalizeBaseURL(sdk, baseURL))
		record.Set("api_key", apiKey)
		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusBadRequest, "Failed to create provider: "+err.Error())
		}
		_ = rt.Reload(app)
		p := aiprovider.FromRecord(record)
		return writeJSON(e, http.StatusCreated, providerJSON(p))
	}
}

func handlePatchProvider(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		id := strings.TrimSpace(e.Request.PathValue("id"))
		record, err := app.FindRecordById(aiprovider.CollectionName, id)
		if err != nil {
			return writeError(e, http.StatusNotFound, "Provider not found.")
		}
		var req providerWriteRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		sdk := record.GetString("sdk")
		if req.SDK != nil {
			sdk = strings.TrimSpace(*req.SDK)
			if !aiprovider.ValidSDK(sdk) {
				return writeError(e, http.StatusBadRequest, "sdk must be openai, openrouter, google_vision, or mistral.")
			}
			if !aiprovider.IsLLM(sdk) {
				if settings, err := config.FindSettingsRecord(app); err == nil {
					for _, field := range []string{"extract_provider_id", "chat_provider_id", "search_provider_id"} {
						if strings.TrimSpace(settings.GetString(field)) == record.Id {
							return writeError(e, http.StatusConflict, "Provider is bound to extraction, chat, or search and must stay openai or openrouter.")
						}
					}
				}
			}
			record.Set("sdk", sdk)
		}
		if req.Alias != nil {
			alias := strings.TrimSpace(*req.Alias)
			if alias == "" {
				return writeError(e, http.StatusBadRequest, "alias is required.")
			}
			if err := aiprovider.EnsureUniqueAlias(app, alias, record.Id); err != nil {
				return writeError(e, http.StatusBadRequest, err.Error())
			}
			record.Set("alias", alias)
		}
		if req.BaseURL != nil {
			record.Set("base_url", aiprovider.NormalizeBaseURL(sdk, *req.BaseURL))
		}
		if req.APIKey != nil && strings.TrimSpace(*req.APIKey) != "" {
			record.Set("api_key", strings.TrimSpace(*req.APIKey))
		}
		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusBadRequest, "Failed to update provider: "+err.Error())
		}
		_ = rt.Reload(app)
		return writeJSON(e, http.StatusOK, providerJSON(aiprovider.FromRecord(record)))
	}
}

func handleDeleteProvider(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		id := strings.TrimSpace(e.Request.PathValue("id"))
		record, err := app.FindRecordById(aiprovider.CollectionName, id)
		if err != nil {
			return writeError(e, http.StatusNotFound, "Provider not found.")
		}
		settings, err := config.FindSettingsRecord(app)
		if err == nil && aiprovider.ReferencedBySettings(settings, id) {
			return writeError(e, http.StatusConflict, "Provider is assigned to OCR, extraction, chat, or search. Unassign it first.")
		}
		if err := app.Delete(record); err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to delete provider.")
		}
		_ = rt.Reload(app)
		return writeJSON(e, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func handleListProviderModels(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		id := strings.TrimSpace(e.Request.PathValue("id"))
		p, err := aiprovider.FindByID(app, id)
		if err != nil || p == nil {
			return writeError(e, http.StatusNotFound, "Provider not found.")
		}
		forOCR := strings.EqualFold(strings.TrimSpace(e.Request.URL.Query().Get("for")), "ocr")
		models, err := aiprovider.ListModels(e.Request.Context(), *p, forOCR, nil)
		if err != nil {
			app.Logger().Warn("list provider models", "provider", p.ID, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "Failed to load models from the provider.")
		}
		return writeJSON(e, http.StatusOK, map[string]any{
			"models":  models,
			"sdk":     p.SDK,
			"for_ocr": forOCR,
		})
	}
}
