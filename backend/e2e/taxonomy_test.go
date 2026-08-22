package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type pruneResult struct {
	Tags           int `json:"tags"`
	Correspondents int `json:"correspondents"`
	DocumentTypes  int `json:"document_types"`
}

func TestTaxonomyPruneRemovesOnlyOrphans(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	createEntity := func(collection, name string) string {
		t.Helper()
		status, raw := h.doJSON(t, http.MethodPost, "/api/collections/"+collection+"/records", token, map[string]any{
			"name":          name,
			"name_original": name,
			"user":          h.UserID,
		})
		requireStatus(t, status, http.StatusOK, raw)
		return jsonGetString(mustDecodeMap(t, raw), "id")
	}
	exists := func(collection, id string) bool {
		t.Helper()
		status, _ := h.doJSON(t, http.MethodGet, "/api/collections/"+collection+"/records/"+id, token, nil)
		return status == http.StatusOK
	}

	orphanTag := createEntity("tags", "PruneOrphanTag-"+stamp)
	orphanCorr := createEntity("correspondents", "PruneOrphanCorr-"+stamp)
	orphanType := createEntity("document_types", "PruneOrphanType-"+stamp)
	usedTag := createEntity("tags", "PruneUsedTag-"+stamp)
	usedCorr := createEntity("correspondents", "PruneUsedCorr-"+stamp)
	usedType := createEntity("document_types", "PruneUsedType-"+stamp)

	doc := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	docID := jsonGetString(doc, "id")
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+docID, token, nil)
	})
	h.settleDocuments(t, docID)

	status, raw := h.doJSON(t, http.MethodPatch, "/api/collections/documents/records/"+docID, token, map[string]any{
		"tags":              []string{usedTag},
		"correspondent":     usedCorr,
		"document_type":     usedType,
		"processing_status": "completed",
	})
	requireStatus(t, status, http.StatusOK, raw)

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/taxonomy/prune", token, nil)
	if status == http.StatusOK {
		t.Fatalf("regular user should not prune: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/taxonomy/prune", h.adminUserToken(t), nil)
	requireStatus(t, status, http.StatusOK, raw)
	var result pruneResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode prune: %v body %s", err, raw)
	}
	if result.Tags < 1 || result.Correspondents < 1 || result.DocumentTypes < 1 {
		t.Fatalf("expected each collection to lose at least one orphan, got %+v", result)
	}

	for _, orphan := range []struct{ collection, id string }{
		{"tags", orphanTag},
		{"correspondents", orphanCorr},
		{"document_types", orphanType},
	} {
		if exists(orphan.collection, orphan.id) {
			t.Fatalf("expected orphan %s %s to be removed", orphan.collection, orphan.id)
		}
	}
	for _, used := range []struct{ collection, id string }{
		{"tags", usedTag},
		{"correspondents", usedCorr},
		{"document_types", usedType},
	} {
		if !exists(used.collection, used.id) {
			t.Fatalf("expected referenced %s %s to survive", used.collection, used.id)
		}
	}

	kept := h.getDocument(t, token, docID)
	if jsonGetString(kept, "correspondent") != usedCorr {
		t.Fatalf("document lost its correspondent: %v", kept["correspondent"])
	}
	if jsonGetString(kept, "document_type") != usedType {
		t.Fatalf("document lost its document type: %v", kept["document_type"])
	}
	tags, _ := kept["tags"].([]any)
	if len(tags) != 1 || fmt.Sprint(tags[0]) != usedTag {
		t.Fatalf("document lost its tag: %v", kept["tags"])
	}
}

func TestTaxonomyPruneIsIdempotent(t *testing.T) {
	h := StartShared(t)
	admin := h.adminUserToken(t)

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/taxonomy/prune", admin, nil)
	requireStatus(t, status, http.StatusOK, raw)

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/taxonomy/prune", admin, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var second pruneResult
	if err := json.Unmarshal([]byte(raw), &second); err != nil {
		t.Fatalf("decode prune: %v body %s", err, raw)
	}
	if second.Tags != 0 || second.Correspondents != 0 || second.DocumentTypes != 0 {
		t.Fatalf("expected nothing left to prune, got %+v", second)
	}
}
