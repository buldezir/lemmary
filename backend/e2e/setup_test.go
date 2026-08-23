package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSetupStatusReadyOnSharedHarness(t *testing.T) {
	h := StartShared(t)

	status, raw := h.doJSON(t, http.MethodGet, "/api/app/setup/status", "", nil)
	requireStatus(t, status, http.StatusOK, raw)

	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["needs_admin"] != false {
		t.Fatalf("needs_admin=%v want false", body["needs_admin"])
	}
	if body["needs_config"] != false {
		t.Fatalf("needs_config=%v want false (shared harness seeds keys)", body["needs_config"])
	}
	if body["has_ocr"] != true {
		t.Fatalf("has_ocr=%v", body["has_ocr"])
	}
	if body["has_llm"] != true {
		t.Fatalf("has_llm=%v", body["has_llm"])
	}
}

func TestSetupAdminCreateOnce(t *testing.T) {
	h, err := Start(Options{SkipAuthSeed: true, EmptyAPIKeys: true})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Close()

	status, raw := h.doJSON(t, http.MethodGet, "/api/app/setup/status", "", nil)
	requireStatus(t, status, http.StatusOK, raw)
	var body map[string]any
	_ = json.Unmarshal([]byte(raw), &body)
	if body["needs_admin"] != true {
		t.Fatalf("needs_admin=%v want true", body["needs_admin"])
	}
	if body["needs_config"] != true {
		t.Fatalf("needs_config=%v want true", body["needs_config"])
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/setup/admin", "", map[string]any{
		"email":           "fresh-admin@lemmary.local",
		"password":        "freshpassword123",
		"passwordConfirm": "freshpassword123",
	})
	requireStatus(t, status, http.StatusCreated, raw)

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/setup/status", "", nil)
	requireStatus(t, status, http.StatusOK, raw)
	_ = json.Unmarshal([]byte(raw), &body)
	if body["needs_admin"] != false {
		t.Fatalf("after create needs_admin=%v", body["needs_admin"])
	}
	if body["needs_config"] != true {
		t.Fatalf("after create needs_config=%v want true", body["needs_config"])
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/setup/admin", "", map[string]any{
		"email":           "another@lemmary.local",
		"password":        "anotherpassword1",
		"passwordConfirm": "anotherpassword1",
	})
	if status != http.StatusConflict {
		t.Fatalf("second admin create status %d want 409 body %s", status, raw)
	}

	// Paired users account exists and can finish config via settings (is_admin).
	userAuth := h.authWithPassword(t, "users", "fresh-admin@lemmary.local", "freshpassword123")
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/me", userAuth.Token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var me map[string]any
	if err := json.Unmarshal([]byte(raw), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me["is_admin"] != true {
		t.Fatalf("is_admin=%v want true body %s", me["is_admin"], raw)
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/providers", userAuth.Token, map[string]any{
		"sdk":      "mistral",
		"alias":    "Setup Mistral",
		"base_url": h.Mocks.OCR.URL + "/v1",
		"api_key":  "setup-mistral-key",
	})
	requireStatus(t, status, http.StatusCreated, raw)
	var mistral map[string]any
	_ = json.Unmarshal([]byte(raw), &mistral)

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/providers", userAuth.Token, map[string]any{
		"sdk":      "openai",
		"alias":    "Setup OpenAI",
		"base_url": h.Mocks.OpenAI.URL + "/v1",
		"api_key":  "setup-openai-key",
	})
	requireStatus(t, status, http.StatusCreated, raw)
	var openai map[string]any
	_ = json.Unmarshal([]byte(raw), &openai)

	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/settings", userAuth.Token, map[string]any{
		"ocr_provider_id":     mistral["id"],
		"ocr_model":           "mistral-ocr-latest",
		"extract_provider_id": openai["id"],
		"extract_model":       "e2e-mock",
	})
	requireStatus(t, status, http.StatusOK, raw)

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/setup/status", "", nil)
	requireStatus(t, status, http.StatusOK, raw)
	_ = json.Unmarshal([]byte(raw), &body)
	if body["needs_config"] != false {
		t.Fatalf("after keys needs_config=%v want false", body["needs_config"])
	}
}

func TestSetupAdminValidation(t *testing.T) {
	h, err := Start(Options{SkipAuthSeed: true, EmptyAPIKeys: true})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Close()

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/setup/admin", "", map[string]any{
		"email":           "not-an-email",
		"password":        "short",
		"passwordConfirm": "short",
	})
	if status == http.StatusCreated {
		t.Fatalf("invalid admin should fail: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/setup/admin", "", map[string]any{
		"email":           "ok@lemmary.local",
		"password":        "longenough1",
		"passwordConfirm": "mismatch!!",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("mismatch passwords status %d want 400 body %s", status, raw)
	}
}
