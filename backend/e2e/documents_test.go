package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDocumentsUploadListGetPatchDelete(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	id := jsonGetString(rec, "id")
	if id == "" {
		t.Fatal("missing document id")
	}

	status, raw := h.doJSON(t, http.MethodGet, "/api/collections/documents/records?perPage=50", token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	items := mustDecodeList(t, raw)
	found := false
	for _, item := range items {
		if jsonGetString(item, "id") == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("uploaded document %s not in list", id)
	}

	doc := h.getDocument(t, token, id)
	if jsonGetString(doc, "file") == "" {
		t.Fatal("expected file field")
	}

	status, raw = h.doJSON(t, http.MethodPatch, "/api/collections/documents/records/"+id, token, map[string]any{
		"title":   "Manual Title",
		"purpose": "e2e patch",
	})
	requireStatus(t, status, http.StatusOK, raw)
	var patched map[string]any
	if err := json.Unmarshal([]byte(raw), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if jsonGetString(patched, "title") != "Manual Title" {
		t.Fatalf("title=%q", patched["title"])
	}
	doc = patched

	// Download original file via PocketBase files API.
	fileName := jsonGetString(doc, "file")
	status, body, _ := h.doRaw(t, http.MethodGet, "/api/files/documents/"+id+"/"+fileName, token, nil, "")
	requireStatus(t, status, http.StatusOK, body)
	requireContains(t, body, "Acme Plumbing")

	status, raw = h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+id, token, nil)
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("delete: %s", formatErr(status, raw))
	}
	status, _ = h.doJSON(t, http.MethodGet, "/api/collections/documents/records/"+id, token, nil)
	if status == http.StatusOK {
		t.Fatal("document still exists after delete")
	}
}

// TestAdminUserCanUpload covers #7: paired admin users session owns documents.
func TestAdminUserCanUpload(t *testing.T) {
	h := StartShared(t)
	token := h.adminUserToken(t)
	if h.AdminUserID == "" {
		t.Fatal("missing AdminUserID")
	}
	rec := h.uploadDocument(t, token, h.AdminUserID, fixturePath("sample.txt"))
	id := jsonGetString(rec, "id")
	if id == "" {
		t.Fatal("missing document id")
	}
	if jsonGetString(rec, "user") != h.AdminUserID {
		t.Fatalf("user=%q want %q", jsonGetString(rec, "user"), h.AdminUserID)
	}
	status, raw := h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+id, token, nil)
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("delete: %s", formatErr(status, raw))
	}
}

func TestDocumentsOwnerIsolation(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	id := jsonGetString(rec, "id")

	// Create a second user and ensure they cannot view the first user's document.
	otherEmail := "other-e2e@paperless.local"
	otherPass := "otherpassword123"
	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/users/records", h.superToken(t), map[string]any{
		"email":           otherEmail,
		"password":        otherPass,
		"passwordConfirm": otherPass,
		"verified":        true,
	})
	if status < 200 || status >= 300 {
		// Superuser create via API may need different shape; fall back to app API.
		otherID, err := createAuthRecord(h.App, "users", otherEmail, otherPass)
		if err != nil {
			t.Fatalf("create other user via API (%s) and app (%v)", formatErr(status, raw), err)
		}
		_ = otherID
	}

	otherToken := h.authWithPassword(t, "users", otherEmail, otherPass).Token
	status, raw = h.doJSON(t, http.MethodGet, "/api/collections/documents/records/"+id, otherToken, nil)
	if status == http.StatusOK {
		t.Fatalf("other user should not see document: %s", raw)
	}
}

func TestDocumentsFilterByTitle(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	id := jsonGetString(rec, "id")

	status, raw := h.doJSON(t, http.MethodPatch, "/api/collections/documents/records/"+id, token, map[string]any{
		"title": "UniqueFilterTitleXYZ",
	})
	requireStatus(t, status, http.StatusOK, raw)

	status, raw = h.doJSON(t, http.MethodGet, `/api/collections/documents/records?filter=title~"UniqueFilterTitleXYZ"`, token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	items := mustDecodeList(t, raw)
	if len(items) == 0 {
		t.Fatal("expected filtered hit")
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(raw), &body)
}
