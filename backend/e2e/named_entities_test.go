package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"paperless-go/backend/internal/worker"
)

func TestNamedEntityOwnerIsolation(t *testing.T) {
	h := StartShared(t)
	tokenA := h.userToken(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	typeName := "IsoType-" + stamp
	corrName := "IsoCorr-" + stamp

	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/document_types/records", tokenA, map[string]any{
		"name":          typeName,
		"name_original": typeName,
		"user":          h.UserID,
	})
	requireStatus(t, status, http.StatusOK, raw)
	typeID := jsonGetString(mustDecodeMap(t, raw), "id")
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/document_types/records/"+typeID, tokenA, nil)
	})

	status, raw = h.doJSON(t, http.MethodPost, "/api/collections/correspondents/records", tokenA, map[string]any{
		"name":          corrName,
		"name_original": corrName,
		"user":          h.UserID,
	})
	requireStatus(t, status, http.StatusOK, raw)
	corrID := jsonGetString(mustDecodeMap(t, raw), "id")
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/correspondents/records/"+corrID, tokenA, nil)
	})

	created := mustDecodeMap(t, raw)
	if jsonGetString(created, "user") != h.UserID {
		t.Fatalf("correspondent user=%q want %q", jsonGetString(created, "user"), h.UserID)
	}

	otherEmail := fmt.Sprintf("named-other-%s@paperless.local", stamp)
	otherPass := "otherpassword123"
	status, raw = h.doJSON(t, http.MethodPost, "/api/collections/users/records", h.superToken(t), map[string]any{
		"email":           otherEmail,
		"password":        otherPass,
		"passwordConfirm": otherPass,
		"verified":        true,
	})
	otherID := ""
	if status >= 200 && status < 300 {
		otherID = jsonGetString(mustDecodeMap(t, raw), "id")
	} else {
		var err error
		otherID, err = createAuthRecord(h.App, "users", otherEmail, otherPass)
		if err != nil {
			t.Fatalf("create other user via API (%s) and app (%v)", formatErr(status, raw), err)
		}
	}
	authB := h.authWithPassword(t, "users", otherEmail, otherPass)
	tokenB := authB.Token
	if otherID == "" {
		otherID = jsonGetString(authB.Record, "id")
	}

	status, raw = h.doJSON(t, http.MethodGet, "/api/collections/document_types/records/"+typeID, tokenB, nil)
	if status == http.StatusOK {
		t.Fatalf("other user should not view document type: %s", raw)
	}
	status, raw = h.doJSON(t, http.MethodGet, "/api/collections/correspondents/records/"+corrID, tokenB, nil)
	if status == http.StatusOK {
		t.Fatalf("other user should not view correspondent: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodGet, `/api/collections/document_types/records?filter=name="`+typeName+`"`, tokenB, nil)
	requireStatus(t, status, http.StatusOK, raw)
	if len(mustDecodeList(t, raw)) != 0 {
		t.Fatalf("other user listed user A's document type: %s", raw)
	}
	status, raw = h.doJSON(t, http.MethodGet, `/api/collections/correspondents/records?filter=name="`+corrName+`"`, tokenB, nil)
	requireStatus(t, status, http.StatusOK, raw)
	if len(mustDecodeList(t, raw)) != 0 {
		t.Fatalf("other user listed user A's correspondent: %s", raw)
	}

	ngxAuthA := ngxToken(t, h, UserEmail, UserPassword)
	ngxAuthB := ngxToken(t, h, otherEmail, otherPass)

	status, raw = h.doJSON(t, http.MethodGet, "/api/correspondents/?page_size=100", ngxAuthA, nil)
	requireStatus(t, status, http.StatusOK, raw)
	if !ngxResultsContainName(t, raw, corrName) {
		t.Fatalf("ngx list missing user A's correspondent: %s", raw)
	}
	ngxCorrID := ngxResultIDByName(t, raw, corrName)

	status, raw = h.doJSON(t, http.MethodGet, "/api/correspondents/?page_size=100", ngxAuthB, nil)
	requireStatus(t, status, http.StatusOK, raw)
	if ngxResultsContainName(t, raw, corrName) {
		t.Fatalf("ngx list leaked user A's correspondent to user B: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodGet, fmt.Sprintf("/api/correspondents/%s/", ngxCorrID), ngxAuthB, nil)
	if status == http.StatusOK {
		t.Fatalf("other user should not GET user A's ngx correspondent: %s", raw)
	}

	idA, createdA, err := worker.EnsureNamedEntity(h.App, "correspondents", h.UserID, corrName, corrName)
	if err != nil {
		t.Fatalf("ensure user A: %v", err)
	}
	if createdA || idA != corrID {
		t.Fatalf("user A should reuse existing correspondent id=%s created=%v got id=%s", corrID, createdA, idA)
	}
	idB, createdB, err := worker.EnsureNamedEntity(h.App, "correspondents", otherID, corrName, corrName)
	if err != nil {
		t.Fatalf("ensure user B: %v", err)
	}
	if !createdB || idB == "" || idB == idA {
		t.Fatalf("user B should get a distinct correspondent, created=%v idA=%s idB=%s", createdB, idA, idB)
	}
	t.Cleanup(func() {
		if rec, err := h.App.FindRecordById("correspondents", idB); err == nil {
			_ = h.App.Delete(rec)
		}
	})

	status, raw = h.doJSON(t, http.MethodPost, "/api/document_types/", ngxAuthB, map[string]any{
		"name": "NgxType-" + stamp,
	})
	if status < 200 || status >= 300 {
		t.Fatalf("ngx create document type: %s", formatErr(status, raw))
	}
	createdType := mustDecodeMap(t, raw)
	ngxTypeID := fmt.Sprintf("%v", createdType["id"])
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/document_types/"+ngxTypeID+"/", ngxAuthB, nil)
	})

	records, err := h.App.FindRecordsByFilter("document_types", `name = {:name} && user = {:user}`, "", 1, 0, map[string]any{
		"name": "NgxType-" + stamp,
		"user": otherID,
	})
	if err != nil || len(records) != 1 {
		t.Fatalf("expected ngx-created type owned by user B: err=%v n=%d", err, len(records))
	}

	doc := h.uploadDocument(t, tokenB, otherID, fixturePath("sample.txt"))
	docID := jsonGetString(doc, "id")
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+docID, tokenB, nil)
	})

	status, raw = h.doJSON(t, http.MethodPatch, "/api/collections/documents/records/"+docID, tokenB, map[string]any{
		"document_type": typeID,
		"correspondent": corrID,
	})
	if status == http.StatusOK {
		t.Fatalf("user B should not attach user A's named entities: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodPatch, "/api/collections/documents/records/"+docID, tokenB, map[string]any{
		"document_type": records[0].Id,
	})
	requireStatus(t, status, http.StatusOK, raw)
	updated := mustDecodeMap(t, raw)
	if jsonGetString(updated, "document_type") != records[0].Id {
		t.Fatalf("expected user B's document type, got %s", raw)
	}
}

func ngxToken(t *testing.T, h *Harness, email, password string) string {
	t.Helper()
	status, raw := h.doJSON(t, http.MethodPost, "/api/token/", "", map[string]string{
		"username": email,
		"password": password,
	})
	requireStatus(t, status, http.StatusOK, raw)
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(raw), &tok); err != nil || tok.Token == "" {
		t.Fatalf("token response: %s", raw)
	}
	return "Token " + tok.Token
}

func ngxResultsContainName(t *testing.T, raw, name string) bool {
	t.Helper()
	return ngxResultIDByName(t, raw, name) != ""
}

func ngxResultIDByName(t *testing.T, raw, name string) string {
	t.Helper()
	var list struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("decode ngx list: %v body %s", err, raw)
	}
	for _, item := range list.Results {
		if jsonGetString(item, "name") == name {
			return fmt.Sprintf("%v", item["id"])
		}
	}
	return ""
}
