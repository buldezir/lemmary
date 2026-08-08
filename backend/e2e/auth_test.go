package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAuthUserLogin(t *testing.T) {
	h := StartShared(t)
	auth := h.authWithPassword(t, "users", UserEmail, UserPassword)
	if auth.Token == "" {
		t.Fatal("expected token")
	}
	if jsonGetString(auth.Record, "email") != UserEmail {
		t.Fatalf("email=%q", auth.Record["email"])
	}
}

func TestAuthSuperuserLogin(t *testing.T) {
	h := StartShared(t)
	auth := h.authWithPassword(t, "_superusers", SuperEmail, SuperPassword)
	if auth.Token == "" {
		t.Fatal("expected token")
	}
}

func TestEnsureUserCreatesPairedAccount(t *testing.T) {
	h, err := Start(Options{SkipAuthSeed: true, EmptyAPIKeys: true})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Close()

	superID, err := createAuthRecord(h.App, "_superusers", "legacy-admin@paperless.local", "legacypassword1")
	if err != nil {
		t.Fatalf("create super only: %v", err)
	}
	_ = superID

	superAuth := h.authWithPassword(t, "_superusers", "legacy-admin@paperless.local", "legacypassword1")
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/ensure-user", superAuth.Token, map[string]any{
		"password": "legacypassword1",
	})
	requireStatus(t, status, http.StatusOK, raw)

	userAuth := h.authWithPassword(t, "users", "legacy-admin@paperless.local", "legacypassword1")
	if userAuth.Token == "" {
		t.Fatal("expected users token after ensure-user")
	}
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/me", userAuth.Token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var me map[string]any
	if err := json.Unmarshal([]byte(raw), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me["is_admin"] != true {
		t.Fatalf("is_admin=%v want true", me["is_admin"])
	}
}

func TestAuthBadPassword(t *testing.T) {
	h := StartShared(t)
	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/users/auth-with-password", "", map[string]string{
		"identity": UserEmail,
		"password": "wrong-password",
	})
	if status == http.StatusOK {
		t.Fatalf("expected auth failure, got %s", raw)
	}
}

func TestUnauthenticatedDocumentsRejected(t *testing.T) {
	h := StartShared(t)
	status, raw := h.doJSON(t, http.MethodGet, "/api/collections/documents/records", "", nil)
	if status == http.StatusOK {
		t.Fatalf("expected rejection, got %s", raw)
	}
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		// PocketBase may return 400 with auth error depending on rules/guard.
		var body map[string]any
		_ = json.Unmarshal([]byte(raw), &body)
		if status < 400 {
			t.Fatalf("expected client error, got %d %s", status, raw)
		}
	}
}
