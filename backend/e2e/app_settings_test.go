package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAppSettingsSuperuserOnly(t *testing.T) {
	h := StartShared(t)

	userTok := h.userToken(t)
	status, raw := h.doJSON(t, http.MethodGet, "/api/app/settings", userTok, nil)
	if status == http.StatusOK {
		t.Fatalf("regular user should not read settings: %s", raw)
	}

	superTok := h.superToken(t)
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/settings", superTok, nil)
	requireStatus(t, status, http.StatusOK, raw)

	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if settings["extract_model"] != "e2e-mock" {
		t.Fatalf("extract_model=%v", settings["extract_model"])
	}
	if _, ok := settings["openai_api_key"]; ok {
		t.Fatal("raw openai_api_key should not be exposed")
	}
	if settings["extract_provider_id"] == nil || settings["extract_provider_id"] == "" {
		t.Fatalf("expected extract_provider_id in %v", settings)
	}

	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/settings", userTok, map[string]any{
		"extract_model": "should-fail",
	})
	if status == http.StatusOK {
		t.Fatalf("regular user should not patch settings: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/settings", superTok, map[string]any{
		"extract_model":              "e2e-mock-updated",
		"deep_search_languages":      "en,de",
		"processing_result_language": "en",
	})
	requireStatus(t, status, http.StatusOK, raw)

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/settings", superTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
	_ = json.Unmarshal([]byte(raw), &settings)
	if settings["extract_model"] != "e2e-mock-updated" {
		t.Fatalf("extract_model not updated: %v", settings["extract_model"])
	}

	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/settings", superTok, map[string]any{
		"extract_model":              "e2e-mock",
		"deep_search_languages":      "en",
		"processing_result_language": "",
	})
	requireStatus(t, status, http.StatusOK, raw)
}

func TestAppSettingsPairedAdminUser(t *testing.T) {
	h := StartShared(t)

	adminTok := h.adminUserToken(t)
	status, raw := h.doJSON(t, http.MethodGet, "/api/app/me", adminTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var me map[string]any
	if err := json.Unmarshal([]byte(raw), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me["is_admin"] != true {
		t.Fatalf("paired admin is_admin=%v want true", me["is_admin"])
	}

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/settings", adminTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
}

func TestAppMeRegularUserNotAdmin(t *testing.T) {
	h := StartShared(t)
	userTok := h.userToken(t)
	status, raw := h.doJSON(t, http.MethodGet, "/api/app/me", userTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var me map[string]any
	if err := json.Unmarshal([]byte(raw), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me["is_admin"] != false {
		t.Fatalf("regular user is_admin=%v want false", me["is_admin"])
	}
}

func TestAppProvidersCRUD(t *testing.T) {
	h := StartShared(t)
	superTok := h.superToken(t)
	userTok := h.userToken(t)

	status, raw := h.doJSON(t, http.MethodGet, "/api/app/providers", userTok, nil)
	if status == http.StatusOK {
		t.Fatalf("regular user should not list providers: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/providers", superTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	providers, _ := body["providers"].([]any)
	if len(providers) < 2 {
		t.Fatalf("expected seeded providers, got %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/providers", superTok, map[string]any{
		"sdk":      "openrouter",
		"alias":    "E2E OpenRouter",
		"base_url": h.Mocks.OpenAI.URL + "/v1",
		"api_key":  "e2e-or-key",
	})
	requireStatus(t, status, http.StatusCreated, raw)
	var created map[string]any
	_ = json.Unmarshal([]byte(raw), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("missing id: %s", raw)
	}
	if created["api_key_set"] != true {
		t.Fatalf("api_key_set=%v", created["api_key_set"])
	}
	if _, ok := created["api_key"]; ok {
		t.Fatal("raw api_key should not be exposed")
	}

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/providers/"+id+"/models?for=llm", superTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
	requireContains(t, raw, "e2e-mock")

	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/providers/"+id, superTok, map[string]any{
		"alias": "E2E OpenRouter renamed",
	})
	requireStatus(t, status, http.StatusOK, raw)
	requireContains(t, raw, "E2E OpenRouter renamed")

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/settings", superTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	inUse, _ := settings["extract_provider_id"].(string)
	if inUse == "" {
		t.Fatal("expected extract_provider_id")
	}
	status, raw = h.doJSON(t, http.MethodDelete, "/api/app/providers/"+inUse, superTok, nil)
	if status != http.StatusConflict {
		t.Fatalf("expected 409 deleting in-use provider, got %d: %s", status, raw)
	}

	status, raw = h.doJSON(t, http.MethodDelete, "/api/app/providers/"+id, superTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
}
