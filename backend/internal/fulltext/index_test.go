package fulltext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testIndex(t *testing.T) *Index {
	t.Helper()
	idx := New()
	if err := idx.Open(t.TempDir()); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func mustPut(t *testing.T, idx *Index, id string, doc map[string]any) {
	t.Helper()
	if err := idx.Put(id, doc); err != nil {
		t.Fatalf("put %s: %v", id, err)
	}
}

func searchIDs(t *testing.T, idx *Index, q Query) []string {
	t.Helper()
	res, err := idx.Search(q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	ids := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		ids = append(ids, h.ID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestSearchANDVsPhrase(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "both", map[string]any{
		FieldUser:    "u1",
		FieldTitle:   "Plumber invoice",
		FieldOCRText: "Paid the plumber invoice in July",
		FieldAll:     "Plumber invoice Paid the plumber invoice in July",
	})
	mustPut(t, idx, "split", map[string]any{
		FieldUser:    "u1",
		FieldTitle:   "Plumber visit",
		FieldOCRText: "Later an invoice arrived by mail",
		FieldAll:     "Plumber visit Later an invoice arrived by mail",
	})

	andHits := searchIDs(t, idx, Query{Text: "plumber invoice", UserID: "u1"})
	if !containsID(andHits, "both") {
		t.Fatalf("AND should match combined doc, got %v", andHits)
	}
	if !containsID(andHits, "split") {
		t.Fatalf("AND should match docs that have both terms, got %v", andHits)
	}

	phraseHits := searchIDs(t, idx, Query{Text: `"plumber invoice"`, UserID: "u1"})
	if !containsID(phraseHits, "both") {
		t.Fatalf("phrase should match combined doc, got %v", phraseHits)
	}
	if containsID(phraseHits, "split") {
		t.Fatalf("phrase should not match split terms, got %v", phraseHits)
	}
}

func TestSearchOwnerIsolation(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "mine", map[string]any{
		FieldUser:  "u1",
		FieldTitle: "Secret lease",
		FieldAll:   "Secret lease",
	})
	mustPut(t, idx, "theirs", map[string]any{
		FieldUser:  "u2",
		FieldTitle: "Secret lease",
		FieldAll:   "Secret lease",
	})

	mine := searchIDs(t, idx, Query{Text: "lease", UserID: "u1"})
	if !containsID(mine, "mine") || containsID(mine, "theirs") {
		t.Fatalf("owner scope: %v", mine)
	}

	all := searchIDs(t, idx, Query{Text: "lease"})
	if !containsID(all, "mine") || !containsID(all, "theirs") {
		t.Fatalf("superuser should see both: %v", all)
	}
}

func TestSearchTagNameAndDateRange(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "tagged", map[string]any{
		FieldUser:         "u1",
		FieldTags:         []string{"tag1"},
		FieldTagNames:     "filter-tag-abc",
		FieldDocumentDate: time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
		FieldTitle:        "Tagged invoice",
		FieldAll:          "filter-tag-abc Tagged invoice",
	})
	mustPut(t, idx, "other", map[string]any{
		FieldUser:         "u1",
		FieldTags:         []string{},
		FieldTagNames:     "",
		FieldDocumentDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		FieldTitle:        "Other invoice",
		FieldAll:          "Other invoice",
	})

	byTag := searchIDs(t, idx, Query{Text: "filter-tag-abc", UserID: "u1"})
	if !containsID(byTag, "tagged") || containsID(byTag, "other") {
		t.Fatalf("tag name search: %v", byTag)
	}

	byTagID := searchIDs(t, idx, Query{Text: "invoice", UserID: "u1", TagIDs: []string{"tag1"}})
	if !containsID(byTagID, "tagged") || containsID(byTagID, "other") {
		t.Fatalf("tag id filter: %v", byTagID)
	}

	inRange := searchIDs(t, idx, Query{
		Text:     "invoice",
		UserID:   "u1",
		DateFrom: "2024-07-01",
		DateTo:   "2024-07-31",
	})
	if !containsID(inRange, "tagged") || containsID(inRange, "other") {
		t.Fatalf("date range: %v", inRange)
	}
}

func TestSearchHighlightSnippet(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "doc1", map[string]any{
		FieldUser:    "u1",
		FieldTitle:   "Invoice",
		FieldOCRText: "Preface text. The plumber invoice for the leak was paid in July. Trailing notes.",
		FieldAll:     "Invoice Preface text. The plumber invoice for the leak was paid in July. Trailing notes.",
	})

	res, err := idx.Search(Query{Text: "plumber", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits=%d", len(res.Hits))
	}
	if !strings.Contains(strings.ToLower(res.Hits[0].OCRSnippet), "plumber") {
		t.Fatalf("expected plumber in snippet %q", res.Hits[0].OCRSnippet)
	}
	if strings.Contains(res.Hits[0].OCRSnippet, "<mark>") {
		t.Fatalf("snippet should be plain text: %q", res.Hits[0].OCRSnippet)
	}
}

func TestSearchStatusFilter(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "done", map[string]any{
		FieldUser:             "u1",
		FieldProcessingStatus: "completed",
		FieldTitle:            "Done invoice",
		FieldAll:              "Done invoice",
	})
	mustPut(t, idx, "pending", map[string]any{
		FieldUser:             "u1",
		FieldProcessingStatus: "pending",
		FieldTitle:            "Pending invoice",
		FieldAll:              "Pending invoice",
	})

	hits := searchIDs(t, idx, Query{Text: "invoice", UserID: "u1", ProcessingStatus: "completed"})
	if !containsID(hits, "done") || containsID(hits, "pending") {
		t.Fatalf("status filter: %v", hits)
	}
}

func TestMappingVersionRebuild(t *testing.T) {
	dir := t.TempDir()
	idx := New()
	if err := idx.Open(dir); err != nil {
		t.Fatal(err)
	}
	mustPut(t, idx, "keep", map[string]any{FieldUser: "u1", FieldTitle: "hello", FieldAll: "hello"})
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	versionPath := filepath.Join(dir, "bleve", "mapping.version")
	if err := os.WriteFile(versionPath, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx2 := New()
	if err := idx2.Open(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx2.Close() })
	if !idx2.NeedsRebuild() {
		t.Fatal("expected rebuild after mapping version mismatch")
	}
	hits := searchIDs(t, idx2, Query{Text: "hello", UserID: "u1"})
	if len(hits) != 0 {
		t.Fatalf("stale index should have been wiped, got %v", hits)
	}
}

func TestParseQueryParts(t *testing.T) {
	got := parseQueryParts(`plumber "invoice paid" leak`)
	if len(got) != 3 {
		t.Fatalf("parts=%v", got)
	}
	if got[0].phrase || got[0].text != "plumber" {
		t.Fatalf("part0=%+v", got[0])
	}
	if !got[1].phrase || got[1].text != "invoice paid" {
		t.Fatalf("part1=%+v", got[1])
	}
	if got[2].text != "leak" {
		t.Fatalf("part2=%+v", got[2])
	}
}

func TestEmptyQuerySkipsIndex(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "doc", map[string]any{FieldUser: "u1", FieldTitle: "x", FieldAll: "x"})
	res, err := idx.Search(Query{Text: "   ", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 || res.Total != 0 {
		t.Fatalf("empty query should not search: %+v", res)
	}
}
