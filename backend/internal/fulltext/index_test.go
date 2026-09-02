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

	// A third term nothing carries: strict finds nothing at all, which is the
	// failure the agent tools hit constantly — the model guesses one keyword
	// wrong and the whole search comes back empty.
	strictThree := searchIDs(t, idx, Query{Text: "plumber invoice bathroom", UserID: "u1"})
	if len(strictThree) != 0 {
		t.Fatalf("strict AND should require every term, got %v", strictThree)
	}
	relaxedThree := searchIDs(t, idx, Query{Text: "plumber invoice bathroom", UserID: "u1", Relaxed: true})
	if !containsID(relaxedThree, "both") {
		t.Fatalf("relaxed should match 2 of 3 terms, got %v", relaxedThree)
	}

	// Two of the three terms is the floor the first rung asks for, and it is
	// the requirement reported back while that rung answers.
	firstRung := mustSearch(t, idx, Query{Text: "plumber invoice bathroom", UserID: "u1", Relaxed: true})
	if firstRung.Required != 2 || firstRung.Terms != 3 {
		t.Fatalf("terms=%d required=%d, want 3 and the 2-of-3 floor", firstRung.Terms, firstRung.Required)
	}

	// Only when nothing clears that floor does the wider rung run, and it
	// reports the lower requirement so the caller can say the hits are partial.
	oneOfThree := mustSearch(t, idx, Query{Text: "plumber skylight bathroom", UserID: "u1", Relaxed: true})
	if oneOfThree.Required != 1 || oneOfThree.Terms != 3 {
		t.Fatalf("terms=%d required=%d, want 3 and 1", oneOfThree.Terms, oneOfThree.Required)
	}
	if !containsID(resultIDs(oneOfThree), "both") {
		t.Fatalf("the fallback rung should rescue a one-term match, got %v", resultIDs(oneOfThree))
	}

	// A quoted phrase stays mandatory even in relaxed mode: "split" has both
	// words but not adjacent, and no amount of slack may let it through.
	relaxedPhrase := searchIDs(t, idx, Query{Text: `"plumber invoice" paid`, UserID: "u1", Relaxed: true})
	if containsID(relaxedPhrase, "split") {
		t.Fatalf("relaxed must not drop a quoted phrase, got %v", relaxedPhrase)
	}
	if !containsID(relaxedPhrase, "both") {
		t.Fatalf("relaxed phrase should still match, got %v", relaxedPhrase)
	}
}

func TestSearchRelaxedFuzzyMatchesTypos(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "invoice", map[string]any{
		FieldUser:    "u1",
		FieldTitle:   "Versicherung Police",
		FieldOCRText: "Beitragsrechnung 2024 für die Hausratversicherung, Vertragsnummer 4711.",
		FieldAll:     "Versicherung Police Beitragsrechnung 2024 Hausratversicherung 4711",
	})

	// One transposed letter. Strict matching cannot reach it.
	if hits := searchIDs(t, idx, Query{Text: "Versicherugn", UserID: "u1"}); len(hits) != 0 {
		t.Fatalf("strict should not match a typo, got %v", hits)
	}
	hits := searchIDs(t, idx, Query{Text: "Versicherugn", UserID: "u1", Relaxed: true})
	if !containsID(hits, "invoice") {
		t.Fatalf("relaxed should match one edit away, got %v", hits)
	}

	// Short words get no slack: one edit from "Pole" reaches half the archive.
	if hits := searchIDs(t, idx, Query{Text: "Pole", UserID: "u1", Relaxed: true}); len(hits) != 0 {
		t.Fatalf("short terms must stay exact, got %v", hits)
	}
	// Neither do numbers: 4712 is a different contract, not a misspelt one.
	if hits := searchIDs(t, idx, Query{Text: "4712", UserID: "u1", Relaxed: true}); len(hits) != 0 {
		t.Fatalf("terms with digits must stay exact, got %v", hits)
	}
}

func TestSearchRelaxedRanksExactAboveFuzzy(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "exact", map[string]any{
		FieldUser:    "u1",
		FieldTitle:   "Versicherung",
		FieldOCRText: "Versicherung",
		FieldAll:     "Versicherung",
	})
	mustPut(t, idx, "fuzzy", map[string]any{
		FieldUser:    "u1",
		FieldTitle:   "Versicherunh",
		FieldOCRText: "Versicherunh",
		FieldAll:     "Versicherunh",
	})

	// The fuzzy leg only exists on the fallback rung, so the query has to be
	// one the first rung cannot answer: neither document carries both terms.
	res := mustSearch(t, idx, Query{Text: "Versicherung Zahnarztrechnung", UserID: "u1", Relaxed: true})
	if res.Required != 1 {
		t.Fatalf("expected the fallback rung, required=%d", res.Required)
	}
	hits := resultIDs(res)
	if len(hits) != 2 {
		t.Fatalf("relaxed should reach both, got %v", hits)
	}
	if hits[0] != "exact" {
		t.Fatalf("exact match must outrank the fuzzy one, got %v", hits)
	}
}

func TestSearchHighlightReturnsFragments(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "doc1", map[string]any{
		FieldUser:  "u1",
		FieldTitle: "Invoice",
		FieldOCRText: "Preface text. The plumber invoice for the leak was paid in July. " +
			strings.Repeat("Filler sentence with nothing of interest. ", 40) +
			"A second plumber visit followed in September.",
		FieldAll: "Invoice plumber",
	})

	res, err := idx.Search(Query{Text: "plumber", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits=%d", len(res.Hits))
	}
	hit := res.Hits[0]
	if len(hit.OCRFragments) == 0 {
		t.Fatal("expected at least one OCR fragment")
	}
	if hit.OCRSnippet != hit.OCRFragments[0] {
		t.Fatalf("snippet %q should be the first fragment %q", hit.OCRSnippet, hit.OCRFragments[0])
	}
	for _, frag := range hit.OCRFragments {
		if strings.Contains(frag, "<mark>") {
			t.Fatalf("fragments should be plain text: %q", frag)
		}
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

// synonymBag is the query shape that motivated relaxed matching: a model told
// to "expand the request into concrete keywords" emits alternative names for
// one thing, and strict AND asks for a document that is all of them at once.
const synonymBag = "purchase order receipt invoice payment amount"

func mustSearch(t *testing.T, idx *Index, q Query) Result {
	t.Helper()
	res, err := idx.Search(q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	return res
}

func resultIDs(res Result) []string {
	ids := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		ids = append(ids, h.ID)
	}
	return ids
}

func ocrDoc(title, ocr string) map[string]any {
	return map[string]any{
		FieldUser:    "u1",
		FieldTitle:   title,
		FieldOCRText: ocr,
		FieldAll:     title + " " + ocr,
	}
}

func putSynonymBagDocs(t *testing.T, idx *Index) {
	t.Helper()
	mustPut(t, idx, "po", ocrDoc("Purchase order 4711", "Bestellung an den Lieferanten"))
	mustPut(t, idx, "rcpt", ocrDoc("Receipt", "Kassenbon vom Baumarkt"))
	mustPut(t, idx, "inv", ocrDoc("Invoice", "Rechnung des Klempners"))
	mustPut(t, idx, "pay", ocrDoc("Payment confirmation", "Ueberweisung ausgefuehrt"))
}

func TestRelaxedFindsSynonymBag(t *testing.T) {
	idx := testIndex(t)
	putSynonymBagDocs(t, idx)

	strict := mustSearch(t, idx, Query{Text: synonymBag, UserID: "u1"})
	if len(strict.Hits) != 0 {
		t.Fatalf("strict AND should find nothing for a synonym bag, got %v", resultIDs(strict))
	}
	if strict.Terms != 0 || strict.Required != 0 {
		t.Fatalf("strict path should not report term counts: %+v", strict)
	}

	relaxed := mustSearch(t, idx, Query{Text: synonymBag, UserID: "u1", Relaxed: true})
	for _, want := range []string{"po", "rcpt", "inv", "pay"} {
		if !containsID(resultIDs(relaxed), want) {
			t.Fatalf("relaxed should find %s, got %v", want, resultIDs(relaxed))
		}
	}
	if relaxed.Terms != 6 || relaxed.Required != 1 {
		t.Fatalf("terms=%d required=%d, want 6 and 1", relaxed.Terms, relaxed.Required)
	}
}

// TestRelaxedTierAKeepsPrecision is the guard against "simplifying" the ladder
// into a single min=1 query: when most terms do match, the near misses must
// stay out.
func TestRelaxedTierAKeepsPrecision(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "all", ocrDoc("Plumber invoice", "Paid the plumber invoice in July"))
	mustPut(t, idx, "one", ocrDoc("Plumber visit", "The plumber came by"))

	res := mustSearch(t, idx, Query{Text: "plumber invoice july", UserID: "u1", Relaxed: true})
	if !containsID(resultIDs(res), "all") {
		t.Fatalf("should match the document carrying every term, got %v", resultIDs(res))
	}
	if containsID(resultIDs(res), "one") {
		t.Fatalf("one term out of three should not be enough while better hits exist: %v", resultIDs(res))
	}
	if res.Required != 2 {
		t.Fatalf("required=%d, want 2 of 3", res.Required)
	}
}

// TestRelaxedRanksMoreTermsFirst pins bleve's disjunction coord factor
// (countMatch/countTotal). Without it the fallback rung would be unordered
// noise, and a bleve upgrade that dropped it should fail here.
func TestRelaxedRanksMoreTermsFirst(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "many", ocrDoc("Ledger", "purchase order receipt invoice"))
	mustPut(t, idx, "few", ocrDoc("Ledger", "amount"))

	res := mustSearch(t, idx, Query{Text: synonymBag, UserID: "u1", Relaxed: true})
	if res.Required != 1 {
		t.Fatalf("expected the fallback rung, required=%d", res.Required)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("hits=%v", resultIDs(res))
	}
	if res.Hits[0].ID != "many" {
		t.Fatalf("the document carrying more terms should rank first, got %v", resultIDs(res))
	}
}

func TestRelaxedKeepsPhraseMandatory(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "phrase", ocrDoc("Ledger", "purchase order for one invoice"))
	mustPut(t, idx, "loose", ocrDoc("Ledger", "invoice receipt payment"))

	res := mustSearch(t, idx, Query{
		Text:    `"purchase order" invoice receipt payment`,
		UserID:  "u1",
		Relaxed: true,
	})
	if !containsID(resultIDs(res), "phrase") {
		t.Fatalf("should match the document containing the phrase, got %v", resultIDs(res))
	}
	if containsID(resultIDs(res), "loose") {
		t.Fatalf("a quoted phrase stays mandatory in every rung, got %v", resultIDs(res))
	}
}

func TestRelaxedDemotesUnclosedQuote(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "inv", ocrDoc("Invoice", "Rechnung des Klempners"))

	unbalanced := `invoice "purchase order`

	strict := mustSearch(t, idx, Query{Text: unbalanced, UserID: "u1"})
	if len(strict.Hits) != 0 {
		t.Fatalf("strict path still honours the phrase, got %v", resultIDs(strict))
	}

	relaxed := mustSearch(t, idx, Query{Text: unbalanced, UserID: "u1", Relaxed: true})
	if !containsID(resultIDs(relaxed), "inv") {
		t.Fatalf("an unterminated quote is a typo, not an instruction: %v", resultIDs(relaxed))
	}
}

func TestRelaxedFuzzyOnlyInFallback(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "inv", ocrDoc("Invoice", "Paid the plumber invoice"))
	mustPut(t, idx, "ref", ocrDoc("Reference 12346", "Order reference"))
	mustPut(t, idx, "bill", ocrDoc("Bill", "The bill arrived"))

	typo := mustSearch(t, idx, Query{Text: "invoce", UserID: "u1", Relaxed: true})
	if !containsID(resultIDs(typo), "inv") {
		t.Fatalf("one edit of slack should reach invoice, got %v", resultIDs(typo))
	}

	// A digit-bearing term is an id, an amount or a date: a near miss there is a
	// different document, not the same one spelled badly.
	digits := mustSearch(t, idx, Query{Text: "12345", UserID: "u1", Relaxed: true})
	if len(digits.Hits) != 0 {
		t.Fatalf("digits must not match fuzzily, got %v", resultIDs(digits))
	}

	// Too short: one edit reaches most of the dictionary from three runes.
	short := mustSearch(t, idx, Query{Text: "bil", UserID: "u1", Relaxed: true})
	if len(short.Hits) != 0 {
		t.Fatalf("short terms must not match fuzzily, got %v", resultIDs(short))
	}

	// Fuzziness must not creep into the first rung: a query that matches
	// exactly is answered there, and reports the stricter floor.
	exact := mustSearch(t, idx, Query{Text: "plumber invoice", UserID: "u1", Relaxed: true})
	if !containsID(resultIDs(exact), "inv") {
		t.Fatalf("exact query should match, got %v", resultIDs(exact))
	}
	if exact.Required != 2 {
		t.Fatalf("required=%d, want the strict floor of 2", exact.Required)
	}
}

func TestRelaxedRespectsFilters(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "mine", map[string]any{
		FieldUser:         "u1",
		FieldTitle:        "Invoice",
		FieldDocumentDate: time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
		FieldAll:          "Invoice",
	})
	mustPut(t, idx, "stale", map[string]any{
		FieldUser:         "u1",
		FieldTitle:        "Receipt",
		FieldDocumentDate: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		FieldAll:          "Receipt",
	})
	mustPut(t, idx, "theirs", map[string]any{
		FieldUser:         "u2",
		FieldTitle:        "Invoice",
		FieldDocumentDate: time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
		FieldAll:          "Invoice",
	})

	owned := mustSearch(t, idx, Query{Text: synonymBag, UserID: "u1", Relaxed: true})
	if containsID(resultIDs(owned), "theirs") {
		t.Fatalf("relaxing text must never relax ownership: %v", resultIDs(owned))
	}

	dated := mustSearch(t, idx, Query{
		Text:     synonymBag,
		UserID:   "u1",
		DateFrom: "2024-07-01",
		DateTo:   "2024-07-31",
		Relaxed:  true,
	})
	if !containsID(resultIDs(dated), "mine") || containsID(resultIDs(dated), "stale") {
		t.Fatalf("date range must survive both rungs: %v", resultIDs(dated))
	}
}

// TestRelaxedDoesNotEscalatePastEnd pins the escalation predicate: paging past
// the end of a result set empties the hit slice, and widening the query there
// would swap the corpus under the caller.
func TestRelaxedDoesNotEscalatePastEnd(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "all", ocrDoc("Plumber invoice", "Paid the plumber invoice"))
	mustPut(t, idx, "one", ocrDoc("Plumber visit", "The plumber came by"))

	res := mustSearch(t, idx, Query{Text: "plumber invoice", UserID: "u1", Relaxed: true, Offset: 10})
	if len(res.Hits) != 0 {
		t.Fatalf("offset past the end should be empty, got %v", resultIDs(res))
	}
	if res.Total != 1 {
		t.Fatalf("total=%d, want the first rung's 1", res.Total)
	}
	if res.Required != 2 {
		t.Fatalf("required=%d, want no escalation", res.Required)
	}
}

func TestRelaxedFallbackLimit(t *testing.T) {
	idx := testIndex(t)
	terms := []string{"purchase", "order", "receipt", "invoice", "payment", "amount"}
	for n := 0; n < 15; n++ {
		mustPut(t, idx, fmt.Sprintf("doc%02d", n), ocrDoc("Ledger", terms[n%len(terms)]))
	}

	res := mustSearch(t, idx, Query{Text: synonymBag, UserID: "u1", Relaxed: true, Limit: MaxSearchLimit})
	if res.Required != 1 {
		t.Fatalf("expected the fallback rung, required=%d", res.Required)
	}
	if len(res.Hits) != relaxedFallbackLimit {
		t.Fatalf("hits=%d, want the fallback cap of %d", len(res.Hits), relaxedFallbackLimit)
	}
	if res.Total != 15 {
		t.Fatalf("total=%d, want all 15 reported", res.Total)
	}
}

func TestMinShouldMatch(t *testing.T) {
	for _, tc := range []struct{ n, want int }{
		{0, 0}, {1, 1}, {2, 2}, {3, 2}, {4, 3}, {5, 4}, {6, 5}, {10, 7},
	} {
		if got := minShouldMatch(tc.n); got != tc.want {
			t.Errorf("minShouldMatch(%d)=%d, want %d", tc.n, got, tc.want)
		}
	}
}
