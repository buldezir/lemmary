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

// invalidSDKMessage names every SDK the API accepts.
//
// Built from the list rather than written out: this sentence was a literal in
// two handlers, and both still said "openai, openrouter, google_vision, or
// mistral" long enough for a third and fourth SDK to be a real prospect.
func invalidSDKMessage() string {
	return "sdk must be one of " + strings.Join(aiprovider.ValidSDKs, ", ") + "."
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
		if refused, err := refuseWhenManaged(e, rt); refused {
			return err
		}
		var req providerWriteRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		sdk := ""
		if req.SDK != nil {
			sdk = strings.TrimSpace(*req.SDK)
		}
		if !aiprovider.ValidSDK(sdk) {
			return writeError(e, http.StatusBadRequest, invalidSDKMessage())
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
		if apiKey == "" && aiprovider.RequiresAPIKey(sdk) {
			return writeError(e, http.StatusBadRequest, "api_key is required.")
		}
		baseURL := ""
		if req.BaseURL != nil {
			baseURL = strings.TrimSpace(*req.BaseURL)
		}
		// A local OCR engine carries an address where a hosted one carries a
		// credential, and there is no public endpoint to fall back on, so the
		// requirement moves rather than disappearing. NormalizeBaseURL fills in
		// the compose default, so this only fires if one was blanked on purpose.
		if aiprovider.RequiresBaseURL(sdk) && aiprovider.NormalizeBaseURL(sdk, baseURL) == "" {
			return writeError(e, http.StatusBadRequest, "base_url is required for a local OCR provider.")
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
		p := aiprovider.FromRecord(record)
		return writeJSON(e, http.StatusCreated, providerJSON(p))
	}
}

// llmBindingFields are the settings bindings that only an LLM SDK can serve.
// OCR is deliberately absent: it is the one binding google_vision exists for.
// So is the embedding binding, which has its own predicate below -- the local
// SDK embeds without chatting, so the two questions have different answers.
//
// search_helper_provider_id is here because applySettingsPatch already refuses
// a non-LLM provider on write; without it there, a bound helper provider could
// be switched to google_vision through this handler and leave Deep Search's
// bulk reading pointed at an endpoint that cannot serve it.
var llmBindingFields = []string{
	"extract_provider_id", "chat_provider_id", "search_provider_id", "search_helper_provider_id",
}

// embeddingBindingField is checked against CanEmbed rather than IsLLM: the
// embedding client speaks the OpenAI-shaped /embeddings API, which
// google_vision has no equivalent of, so switching a bound provider to it would
// leave Deep Search's dense half calling an endpoint that does not exist and
// nothing would say so until a search came back thin.
const embeddingBindingField = "embedding_provider_id"

// ocrBindingField is checked against CanOCR, which admits everything but the
// local SDK. It needed no guard while every SDK but google_vision could read a
// document and google_vision was the one this binding existed for; a local
// endpoint is the first SDK that can be bound here and do nothing.
const ocrBindingField = "ocr_provider_id"

func boundTo(settings *core.Record, providerID string, fields ...string) bool {
	if settings == nil || strings.TrimSpace(providerID) == "" {
		return false
	}
	for _, field := range fields {
		if strings.TrimSpace(settings.GetString(field)) == providerID {
			return true
		}
	}
	return false
}

func handlePatchProvider(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if refused, err := refuseWhenManaged(e, rt); refused {
			return err
		}
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
				return writeError(e, http.StatusBadRequest, invalidSDKMessage())
			}
			if !aiprovider.IsLLM(sdk) || !aiprovider.CanEmbed(sdk) || !aiprovider.CanOCR(sdk) {
				// A failed settings lookup must not skip these guards:
				// proceeding would let a bound provider become an SDK that
				// cannot serve what it is bound to.
				settings, err := config.FindSettingsRecord(app, rt.Env())
				if err != nil {
					app.Logger().Error("provider patch: settings lookup failed", "error", err)
					return writeError(e, http.StatusInternalServerError, "Failed to verify provider usage.")
				}
				if !aiprovider.IsLLM(sdk) && boundTo(settings, record.Id, llmBindingFields...) {
					return writeError(e, http.StatusConflict, "Provider is bound to extraction, chat, or search and must stay an LLM SDK ("+strings.Join(aiprovider.LLMSDKs(), ", ")+").")
				}
				if !aiprovider.CanEmbed(sdk) && boundTo(settings, record.Id, embeddingBindingField) {
					return writeError(e, http.StatusConflict, "Provider is bound to embeddings and must stay an SDK that can embed ("+strings.Join(aiprovider.EmbeddingSDKs(), ", ")+").")
				}
				if !aiprovider.CanOCR(sdk) && boundTo(settings, record.Id, ocrBindingField) {
					return writeError(e, http.StatusConflict, "Provider is bound to OCR and must stay an SDK that can read a document ("+strings.Join(aiprovider.OCRSDKs(), ", ")+").")
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
		return writeJSON(e, http.StatusOK, providerJSON(aiprovider.FromRecord(record)))
	}
}

func handleDeleteProvider(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if refused, err := refuseWhenManaged(e, rt); refused {
			return err
		}
		id := strings.TrimSpace(e.Request.PathValue("id"))
		record, err := app.FindRecordById(aiprovider.CollectionName, id)
		if err != nil {
			return writeError(e, http.StatusNotFound, "Provider not found.")
		}
		// A failed settings lookup must not skip the in-use check: deleting a
		// provider still bound to OCR/extraction/chat/search/embeddings leaves
		// dangling *_provider_id values in settings.
		settings, err := config.FindSettingsRecord(app, rt.Env())
		if err != nil {
			app.Logger().Error("provider delete: settings lookup failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to verify provider usage.")
		}
		if aiprovider.ReferencedBySettings(settings, id) {
			return writeError(e, http.StatusConflict, "Provider is assigned to OCR, extraction, chat, search, or embeddings. Unassign it first.")
		}
		if err := app.Delete(record); err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to delete provider.")
		}
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
		purpose := aiprovider.ParseModelPurpose(e.Request.URL.Query().Get("for"))
		models, err := aiprovider.ListModels(e.Request.Context(), *p, purpose, nil, app.Logger().With("component", "ai"))
		if err != nil {
			app.Logger().Warn("list provider models", "provider", p.ID, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "Failed to load models from the provider.")
		}
		return writeJSON(e, http.StatusOK, map[string]any{
			"models": models,
			"sdk":    p.SDK,
			"for":    string(purpose),
			// Kept for the frontend that shipped before `for` existed.
			"for_ocr": purpose == aiprovider.PurposeOCR,
		})
	}
}
