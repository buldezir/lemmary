package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

func TestCascadeDeleteEnabledOnUserOwnedRelations(t *testing.T) {
	h := StartShared(t)
	for _, tc := range []struct{ collection, field string }{
		{"documents", "user"},
		{"document_types", "user"},
		{"correspondents", "user"},
		{"processing_jobs", "document"},
	} {
		coll, err := h.App.FindCollectionByNameOrId(tc.collection)
		if err != nil {
			t.Fatalf("%s: %v", tc.collection, err)
		}
		rel, ok := coll.Fields.GetByName(tc.field).(*core.RelationField)
		if !ok || rel == nil {
			t.Fatalf("%s.%s is not a relation field", tc.collection, tc.field)
		}
		if !rel.CascadeDelete {
			t.Errorf("%s.%s CascadeDelete=false, want true", tc.collection, tc.field)
		}
	}
}

func TestUserDeleteCascadesOwnedRecords(t *testing.T) {
	h := StartShared(t)
	super := h.superToken(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	email := fmt.Sprintf("cascade-%s@lemmary.local", stamp)
	pass := "cascadepassword123"

	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/users/records", super, map[string]any{
		"email":           email,
		"password":        pass,
		"passwordConfirm": pass,
		"verified":        true,
	})
	userID := ""
	if status >= 200 && status < 300 {
		userID = jsonGetString(mustDecodeMap(t, raw), "id")
	} else {
		var err error
		userID, err = createAuthRecord(h.App, "users", email, pass)
		if err != nil {
			t.Fatalf("create cascade user via API (%s) and app (%v)", formatErr(status, raw), err)
		}
	}
	token := h.authWithPassword(t, "users", email, pass).Token
	if userID == "" {
		t.Fatal("missing cascade user id")
	}

	typeName := "CascadeType-" + stamp
	status, raw = h.doJSON(t, http.MethodPost, "/api/collections/document_types/records", token, map[string]any{
		"name":          typeName,
		"name_original": typeName,
		"user":          userID,
	})
	requireStatus(t, status, http.StatusOK, raw)
	typeID := jsonGetString(mustDecodeMap(t, raw), "id")

	corrName := "CascadeCorr-" + stamp
	status, raw = h.doJSON(t, http.MethodPost, "/api/collections/correspondents/records", token, map[string]any{
		"name":          corrName,
		"name_original": corrName,
		"user":          userID,
	})
	requireStatus(t, status, http.StatusOK, raw)
	corrID := jsonGetString(mustDecodeMap(t, raw), "id")

	doc := h.uploadDocument(t, token, userID, fixturePath("sample.txt"))
	docID := jsonGetString(doc, "id")
	h.settleDocuments(t, docID)

	jobs, err := h.App.FindRecordsByFilter(
		"processing_jobs",
		"document = {:docId}",
		"",
		50,
		0,
		map[string]any{"docId": docID},
	)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected a processing job for the uploaded document")
	}
	jobIDs := make([]string, 0, len(jobs))
	for _, job := range jobs {
		jobIDs = append(jobIDs, job.Id)
	}

	status, raw = h.doJSON(t, http.MethodDelete, "/api/collections/users/records/"+userID, super, nil)
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("delete user: %s", formatErr(status, raw))
	}

	for _, path := range []string{
		"/api/collections/users/records/" + userID,
		"/api/collections/documents/records/" + docID,
		"/api/collections/document_types/records/" + typeID,
		"/api/collections/correspondents/records/" + corrID,
	} {
		status, raw = h.doJSON(t, http.MethodGet, path, super, nil)
		if status == http.StatusOK {
			t.Fatalf("expected %s to be gone after user delete, got %s", path, raw)
		}
	}
	for _, jobID := range jobIDs {
		if _, err := h.App.FindRecordById("processing_jobs", jobID); err == nil {
			t.Fatalf("processing job %s still exists after user delete", jobID)
		}
	}
}
