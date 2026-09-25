package appapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chatgpt"
	"lemmary/backend/internal/config"
)

type providerResponse struct {
	ID        string `json:"id"`
	SDK       string `json:"sdk"`
	Alias     string `json:"alias"`
	BaseURL   string `json:"base_url"`
	Catalog   string `json:"catalog"`
	APIKeySet bool   `json:"api_key_set"`

	// SignedIn is api_key_set's counterpart for the SDKs that sign in. The
	// token never leaves the server; the account and plan are here so Settings
	// can say whose subscription is spent without decoding a JWT in the browser.
	SignedIn bool   `json:"signed_in"`
	Account  string `json:"account,omitempty"`
	Plan     string `json:"plan,omitempty"`
}

type providerWriteRequest struct {
	SDK     *string `json:"sdk"`
	Alias   *string `json:"alias"`
	BaseURL *string `json:"base_url"`
	APIKey  *string `json:"api_key"`
	Catalog *string `json:"catalog"`
}

// catalogOrDefault validates the pi.dev catalogue an admin picked, falling back
// to the one the SDK implies. Blank is a real answer -- it means no model
// window is known -- but only when it was sent on purpose.
func catalogOrDefault(requested *string, sdk string) (string, bool) {
	if requested == nil {
		return aiprovider.DefaultCatalog(sdk), true
	}
	catalog := strings.TrimSpace(*requested)
	return catalog, aiprovider.ValidCatalog(catalog)
}

// The catalogue list is long enough that naming all of it would bury the point;
// it is a dropdown in the UI, so a bad value means a client sent something the
// form cannot produce.
const invalidCatalogMessage = "catalog must be one of the known model catalogues, or empty."

// invalidSDKMessage is built from the list rather than written out, so a new
// SDK cannot drift out of the sentence that names them.
func invalidSDKMessage() string {
	return "sdk must be one of " + strings.Join(aiprovider.ValidSDKs, ", ") + "."
}

func providerJSON(p aiprovider.Provider) providerResponse {
	out := providerResponse{
		ID:        p.ID,
		SDK:       p.SDK,
		Alias:     p.Alias,
		BaseURL:   p.BaseURL,
		Catalog:   p.Catalog,
		APIKeySet: p.APIKey != "",
		SignedIn:  p.OAuth != "",
	}
	if out.SignedIn {
		// A token that will not parse is a row nobody can use, so it reads as
		// signed out rather than as a signed-in account with no name.
		tok, err := chatgpt.ParseToken(p.OAuth)
		if err != nil || !tok.Valid() {
			out.SignedIn = false
		} else {
			out.Account = firstNonBlank(tok.Email, tok.AccountID)
			out.Plan = tok.Plan
		}
	}
	return out
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
		baseURL := ""
		if req.BaseURL != nil {
			baseURL = strings.TrimSpace(*req.BaseURL)
		}
		if missing := missingProviderField(sdk, apiKey, baseURL); missing != "" {
			return writeError(e, http.StatusBadRequest, missing)
		}

		catalog, ok := catalogOrDefault(req.Catalog, sdk)
		if !ok {
			return writeError(e, http.StatusBadRequest, invalidCatalogMessage)
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
		record.Set("catalog", catalog)
		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusBadRequest, "Failed to create provider: "+err.Error())
		}
		p := aiprovider.FromRecord(record)
		return writeJSON(e, http.StatusCreated, providerJSON(p))
	}
}

// missingProviderField is the 400 message for what an SDK cannot run without,
// or empty. A local OCR engine carries an address where a hosted one carries a
// credential, with no public endpoint to fall back on. NormalizeBaseURL fills in
// the compose default, so the address only goes missing if one was blanked.
func missingProviderField(sdk, apiKey, baseURL string) string {
	if apiKey == "" && aiprovider.RequiresAPIKey(sdk) {
		return "api_key is required."
	}
	if aiprovider.RequiresBaseURL(sdk) && aiprovider.NormalizeBaseURL(sdk, baseURL) == "" {
		return "base_url is required for a local OCR provider."
	}
	return ""
}

// llmBindingFields are the bindings only an LLM SDK can serve. OCR is absent:
// it is the binding google_vision exists for. So is embedding, which the local
// SDK serves without chatting.
var llmBindingFields = []string{
	"extract_provider_id", "research_provider_id",
}

// embeddingBindingField is checked against CanEmbed rather than IsLLM: an SDK
// with no /embeddings endpoint would leave the dense half calling nothing, and
// nothing would say so until a search came back thin.
const embeddingBindingField = "embedding_provider_id"

// ocrBindingField is checked against CanOCR, which admits everything but the
// local SDK, the first one that can be bound here and do nothing.
const ocrBindingField = "ocr_provider_id"

// webSearchBindingField is checked against CanWebSearch, an allow-list: no SDK
// that chats or reads a document also searches the web.
const webSearchBindingField = "websearch_provider_id"

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
		switched := false
		if req.SDK != nil {
			sdk = strings.TrimSpace(*req.SDK)
			if refused, err := refuseProviderSDKSwitch(e, app, rt, record.Id, sdk); refused {
				return err
			}
			switched = sdk != record.GetString("sdk")
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
		patchProviderBaseURL(record, req, sdk, switched)
		if req.APIKey != nil && strings.TrimSpace(*req.APIKey) != "" {
			record.Set("api_key", strings.TrimSpace(*req.APIKey))
		}
		if !patchProviderCatalog(record, req, sdk) {
			return writeError(e, http.StatusBadRequest, invalidCatalogMessage)
		}
		// Only on a switch: a row stored before these rules must stay editable.
		if missing := missingProviderField(sdk, record.GetString("api_key"), record.GetString("base_url")); switched && missing != "" {
			return writeError(e, http.StatusBadRequest, missing)
		}
		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusBadRequest, "Failed to update provider: "+err.Error())
		}
		return writeJSON(e, http.StatusOK, providerJSON(aiprovider.FromRecord(record)))
	}
}

func refuseProviderSDKSwitch(e *core.RequestEvent, app core.App, rt *config.Runtime, providerID, sdk string) (bool, error) {
	if !aiprovider.ValidSDK(sdk) {
		return true, writeError(e, http.StatusBadRequest, invalidSDKMessage())
	}
	if aiprovider.IsLLM(sdk) && aiprovider.CanEmbed(sdk) && aiprovider.CanOCR(sdk) && aiprovider.CanWebSearch(sdk) {
		return false, nil
	}
	// A failed settings lookup must not skip these guards: a bound provider could
	// become an SDK that cannot serve the binding.
	settings, err := config.FindSettingsRecord(app, rt.Env())
	if err != nil {
		app.Logger().Error("provider patch: settings lookup failed", "error", err)
		return true, writeError(e, http.StatusInternalServerError, "Failed to verify provider usage.")
	}
	if !aiprovider.IsLLM(sdk) && boundTo(settings, providerID, llmBindingFields...) {
		return true, writeError(e, http.StatusConflict, "Provider is bound to extraction or research and must stay an LLM SDK ("+strings.Join(aiprovider.LLMSDKs(), ", ")+").")
	}
	if !aiprovider.CanEmbed(sdk) && boundTo(settings, providerID, embeddingBindingField) {
		return true, writeError(e, http.StatusConflict, "Provider is bound to embeddings and must stay an SDK that can embed ("+strings.Join(aiprovider.EmbeddingSDKs(), ", ")+").")
	}
	if !aiprovider.CanOCR(sdk) && boundTo(settings, providerID, ocrBindingField) {
		return true, writeError(e, http.StatusConflict, "Provider is bound to OCR and must stay an SDK that can read a document ("+strings.Join(aiprovider.OCRSDKs(), ", ")+").")
	}
	if !aiprovider.CanWebSearch(sdk) && boundTo(settings, providerID, webSearchBindingField) {
		return true, writeError(e, http.StatusConflict, "Provider is bound to web search and must stay an SDK that can search the web ("+strings.Join(aiprovider.WebSearchSDKs(), ", ")+").")
	}
	return false, nil
}

// A URL belongs to the SDK it was entered for, so a switch that names none
// starts from the new SDK's default.
func patchProviderBaseURL(record *core.Record, req providerWriteRequest, sdk string, switched bool) {
	switch {
	case req.BaseURL != nil:
		record.Set("base_url", aiprovider.NormalizeBaseURL(sdk, *req.BaseURL))
	case switched:
		record.Set("base_url", aiprovider.NormalizeBaseURL(sdk, ""))
	}
}

// An explicit choice wins; otherwise an SDK change only fills a row that never
// had a catalogue. Re-defaulting a row that has one would undo a deliberate pick
// -- the form sends the new default itself when the admin switches SDK and had
// not overridden it.
func patchProviderCatalog(record *core.Record, req providerWriteRequest, sdk string) bool {
	if req.Catalog != nil || (req.SDK != nil && record.GetString("catalog") == "") {
		catalog, ok := catalogOrDefault(req.Catalog, sdk)
		if !ok {
			return false
		}
		record.Set("catalog", catalog)
	}
	return true
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
		// bound provider leaves dangling *_provider_id values in settings.
		settings, err := config.FindSettingsRecord(app, rt.Env())
		if err != nil {
			app.Logger().Error("provider delete: settings lookup failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to verify provider usage.")
		}
		if aiprovider.ReferencedBySettings(settings, id) {
			return writeError(e, http.StatusConflict, "Provider is assigned to OCR, extraction, chat, search, embeddings, or web search. Unassign it first.")
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
