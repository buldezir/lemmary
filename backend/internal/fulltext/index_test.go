package fulltext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/pocketbase/pocketbase/core"
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
	if b, err := os.ReadFile(versionPath); err != nil {
		t.Fatalf("version file should remain until rebuild succeeds: %v", err)
	} else if strings.TrimSpace(string(b)) == MappingVersion {
		t.Fatal("Open must not commit MappingVersion before rebuild completes")
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

func sampleDoc(title string) map[string]any {
	return map[string]any{
		FieldUser:  "u1",
		FieldTitle: title,
		FieldAll:   title,
	}
}

func TestRebuildKeepsLiveIndexOnFailure(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "keep", sampleDoc("hello"))

	t.Cleanup(func() { indexAllHook = nil })
	indexAllHook = func(core.App, bleve.Index) (int, error) {
		return 0, errors.New("injected mid-rebuild failure")
	}

	if _, err := idx.Rebuild(nil); err == nil {
		t.Fatal("expected rebuild error")
	}
	if !idx.Ready() {
		t.Fatal("live index should remain ready after failed rebuild")
	}
	hits := searchIDs(t, idx, Query{Text: "hello", UserID: "u1"})
	if !containsID(hits, "keep") {
		t.Fatalf("expected original docs after failed rebuild, got %v", hits)
	}
}

func TestConcurrentRebuildsSerialize(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "keep", sampleDoc("hello"))

	t.Cleanup(func() { indexAllHook = nil })
	indexAllHook = func(_ core.App, b bleve.Index) (int, error) {
		time.Sleep(30 * time.Millisecond)
		if err := b.Index("keep", sampleDoc("hello")); err != nil {
			return 0, err
		}
		return 1, nil
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for n := 0; n < 2; n++ {
		n := n
		go func() {
			defer wg.Done()
			_, errs[n] = idx.Rebuild(nil)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("rebuild %d: %v", i, err)
		}
	}
	if !idx.Ready() {
		t.Fatal("index not ready after concurrent rebuilds")
	}
	hits := searchIDs(t, idx, Query{Text: "hello", UserID: "u1"})
	if !containsID(hits, "keep") {
		t.Fatalf("expected keep after concurrent rebuilds, got %v", hits)
	}
}

func TestPutWaitsForRebuildThenHitsNewIndex(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "keep", sampleDoc("hello"))

	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { indexAllHook = nil })
	indexAllHook = func(_ core.App, b bleve.Index) (int, error) {
		close(entered)
		<-release
		if err := b.Index("keep", sampleDoc("hello")); err != nil {
			return 0, err
		}
		return 1, nil
	}

	rebuildDone := make(chan error, 1)
	go func() {
		_, err := idx.Rebuild(nil)
		rebuildDone <- err
	}()
	<-entered

	putDone := make(chan error, 1)
	go func() {
		putDone <- idx.Put("late", sampleDoc("hello late"))
	}()

	select {
	case err := <-putDone:
		t.Fatalf("put completed during rebuild: %v", err)
	case <-time.After(40 * time.Millisecond):
	}

	close(release)
	if err := <-rebuildDone; err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if err := <-putDone; err != nil {
		t.Fatalf("put after rebuild: %v", err)
	}

	hits := searchIDs(t, idx, Query{Text: "hello", UserID: "u1"})
	if !containsID(hits, "keep") || !containsID(hits, "late") {
		t.Fatalf("expected keep and late after rebuild, got %v", hits)
	}
}

func TestEnqueueDrainsAndOrders(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "doc", sampleDoc("queued hello"))

	// A delete enqueued after the document exists must win over the earlier
	// state once the queue drains.
	idx.EnqueueDelete("doc")
	idx.WaitIdle()
	if hits := searchIDs(t, idx, Query{Text: "queued", UserID: "u1"}); len(hits) != 0 {
		t.Fatalf("expected delete to apply after WaitIdle, got %v", hits)
	}
}

func TestConcurrentEnqueueAndSearch(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "doc", sampleDoc("racing hello"))

	// Regression: WaitIdle (called by Search) used to wg.Wait concurrently
	// with wg.Add in the enqueue path — an illegal WaitGroup reuse that could
	// panic under load.
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				idx.EnqueueDelete("ghost")
			}
		}()
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				if _, err := idx.Search(Query{Text: "racing", UserID: "u1"}); err != nil {
					t.Errorf("search: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	idx.WaitIdle()
}

func TestIDsByKeywordPaginates(t *testing.T) {
	idx := testIndex(t)
	prev := lookupPageSize
	lookupPageSize = 2
	t.Cleanup(func() { lookupPageSize = prev })

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("d%d", i)
		mustPut(t, idx, id, map[string]any{
			FieldUser:  "u1",
			FieldTags:  []string{"tag-wide"},
			FieldTitle: id,
			FieldAll:   id,
		})
	}

	ids, err := idx.IDsByKeyword(FieldTags, "tag-wide")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 5 {
		t.Fatalf("expected 5 ids across pages, got %v", ids)
	}
}
