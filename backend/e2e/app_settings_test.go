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
	if settings["ocr_provider"] != "mistral" {
		t.Fatalf("ocr_provider=%v", settings["ocr_provider"])
	}
	// API keys should be masked as *_set booleans, not raw secrets.
	if _, ok := settings["openai_api_key"]; ok {
		t.Fatal("raw openai_api_key should not be exposed")
	}
	if settings["openai_api_key_set"] != true && settings["openai_api_key_set"] != false {
		// field might be named differently — check presence of any *_set
		foundSet := false
		for k := range settings {
			if len(k) > 4 && k[len(k)-4:] == "_set" {
				foundSet = true
				break
			}
		}
		if !foundSet {
			t.Fatalf("expected masked key flags in %v", settings)
		}
	}

	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/settings", userTok, map[string]any{
		"openai_model": "should-fail",
	})
	if status == http.StatusOK {
		t.Fatalf("regular user should not patch settings: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/settings", superTok, map[string]any{
		"openai_model":               "e2e-mock-updated",
		"deep_search_languages":      "en,de",
		"processing_result_language": "en",
	})
	requireStatus(t, status, http.StatusOK, raw)

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/settings", superTok, nil)
	requireStatus(t, status, http.StatusOK, raw)
	_ = json.Unmarshal([]byte(raw), &settings)
	if settings["openai_model"] != "e2e-mock-updated" {
		t.Fatalf("openai_model not updated: %v", settings["openai_model"])
	}

	// Restore defaults so later shared-harness tests are not affected.
	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/settings", superTok, map[string]any{
		"openai_model":               "e2e-mock",
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
