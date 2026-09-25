package appapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/retrieval"
)

// stubRetrieverApp adds the two things the search path needs beyond a record
// lookup: somewhere to log, and a filter query that finds nothing.
type stubRetrieverApp struct {
	stubDocuments
}

func (s stubRetrieverApp) Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (s stubRetrieverApp) FindRecordsByFilter(
	_ any, _ string, _ string, _ int, _ int, _ ...dbx.Params,
) ([]*core.Record, error) {
	return nil, nil
}

// The lexical document repeats the query's words; the dense one says the same
// thing as a compound the keyword index cannot reach from the query.
const (
	lexicalText = "Preface. The home insurance premium is 240 EUR per year. Signed."
	denseText   = "Vorwort. Die Versicherungsprämie beträgt 240 EUR pro Jahr. Unterschrift."
)

func hybridIndex(t *testing.T) *fulltext.Index {
	t.Helper()
	idx := fulltext.New()
	if err := idx.Open(t.TempDir()); err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	put := func(id, title, ocr string) {
		t.Helper()
		err := idx.Put(id, map[string]any{
			fulltext.FieldUser:    "u1",
			fulltext.FieldTitle:   title,
			fulltext.FieldOCRText: ocr,
			fulltext.FieldAll:     title + " " + ocr,
		})
		if err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	put("lexical", "Insurance letter", lexicalText)
	put("dense", "Versicherungsschreiben", denseText)
	return idx
}

// hybridRetriever wires the real documents index to an in-memory chunk searcher
// over the same two documents, embedded with the deterministic hash embedder.
func hybridRetriever(t *testing.T, embeds *int) *agentRetriever {
	t.Helper()
	embedder := retrieval.HashEmbedder{}
	chunks, err := retrieval.NewMemoryChunks(context.Background(), embedder, []retrieval.MemoryChunk{
		{DocumentID: "lexical", UserID: "u1", Ord: 0, EndByte: len(lexicalText), Text: lexicalText},
		{DocumentID: "dense", UserID: "u1", Ord: 0, EndByte: len(denseText), Text: denseText},
	})
	if err != nil {
		t.Fatalf("memory chunks: %v", err)
	}

	app := stubRetrieverApp{stubDocuments{recs: map[string]*core.Record{
		"lexical": readableDocument("lexical", "u1", "Insurance letter", lexicalText),
		"dense":   readableDocument("dense", "u1", "Versicherungsschreiben", denseText),
	}}}

	return &agentRetriever{
		app:    app,
		idx:    hybridIndex(t),
		userID: "u1",
		chunks: chunks,
		embedQuery: func(ctx context.Context, text string) ([]float32, error) {
			if embeds != nil {
				*embeds++
			}
			vectors, err := embedder.Embed(ctx, []string{text})
			if err != nil {
				return nil, err
			}
			return vectors[0], nil
		},
	}
}

// The query is one no keyword search can answer, and the control is the same
// search with the chunk index unplugged, which finds nothing at all.
func TestSearchFusesTheDenseListIntoTheLexicalOne(t *testing.T) {
	const query = "Versicherungspraemien"

	control := hybridRetriever(t, nil)
	control.chunks = nil
	control.embedQuery = nil
	lexicalOnly, err := control.search(context.Background(), ai.SearchDocumentsArgs{Query: query})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, hit := range lexicalOnly {
		if hit.ID == "dense" {
			t.Fatal("the keyword index reaches this document on its own; the fixture no longer isolates the dense leg")
		}
	}

	r := hybridRetriever(t, nil)
	hits, err := r.search(context.Background(), ai.SearchDocumentsArgs{Query: query})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := map[string]ai.DocumentHit{}
	for _, hit := range hits {
		found[hit.ID] = hit
	}
	if _, ok := found["dense"]; !ok {
		t.Fatalf("the dense-only document was not retrieved: %#v", hits)
	}
	if len(found["dense"].Passages) == 0 {
		t.Fatalf("a dense hit must still quote a passage: %#v", found["dense"])
	}
	if !strings.Contains(found["dense"].Passages[0].Text, "240 EUR") {
		t.Fatalf("the quoted passage is not the matching one: %q", found["dense"].Passages[0].Text)
	}
}

func TestSearchEmbedsOneQueryOnce(t *testing.T) {
	embeds := 0
	r := hybridRetriever(t, &embeds)

	for i := 0; i < 3; i++ {
		if _, err := r.search(context.Background(), ai.SearchDocumentsArgs{Query: "insurance premium"}); err != nil {
			t.Fatalf("search: %v", err)
		}
	}
	if embeds != 1 {
		t.Fatalf("the same query was embedded %d times", embeds)
	}

	if _, err := r.search(context.Background(), ai.SearchDocumentsArgs{Query: "something else"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if embeds != 2 {
		t.Fatalf("a different query should cost one more embedding, got %d", embeds)
	}
}

// The promise the whole dense path is written around: a retrieval tool that
// fails because the vector store is unhappy is worse than one that answers
// from keywords.
func TestSearchDegradesToLexicalWhenEmbeddingFails(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.embedQuery = func(context.Context, string) ([]float32, error) {
		return nil, io.ErrUnexpectedEOF
	}

	hits, err := r.search(context.Background(), ai.SearchDocumentsArgs{Query: "insurance premium"})
	if err != nil {
		t.Fatalf("a failed embedding must not fail the tool: %v", err)
	}
	if len(hits) == 0 || hits[0].ID != "lexical" {
		t.Fatalf("the keyword result was lost with the dense one: %#v", hits)
	}
}

func TestSearchWithoutADenseIndexIsUnchanged(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.chunks = nil
	r.embedQuery = nil

	hits, err := r.search(context.Background(), ai.SearchDocumentsArgs{Query: "insurance premium"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "lexical" {
		t.Fatalf("keyword-only search changed shape: %#v", hits)
	}
	if len(hits[0].Passages) == 0 {
		t.Fatalf("keyword-only search lost its passages: %#v", hits[0])
	}
}

// The other half of the wiring: a focused read uses the stored chunks to decide
// what to show, so an answer in the middle survives the excerpt.
func TestReadFocusRanksWithTheChunkIndex(t *testing.T) {
	// Long enough that one excerpt cannot hold it, and with the answer far
	// enough in that a head excerpt cannot reach it.
	head := strings.Repeat("Vorspann ohne Bedeutung. ", 800)
	middle := "Die Selbstbeteiligung beträgt 150 EUR je Schadensfall. "
	tail := strings.Repeat("Nachspann ohne Bedeutung. ", 800)
	full := head + middle + tail
	if len(full) <= focusExcerptBytes {
		t.Fatalf("fixture of %d bytes fits one excerpt of %d", len(full), focusExcerptBytes)
	}

	chunks := []retrieval.MemoryChunk{
		{DocumentID: "doc1", UserID: "u1", Ord: 0, StartByte: 0, EndByte: len(head), Text: head},
		{DocumentID: "doc1", UserID: "u1", Ord: 1, StartByte: len(head), EndByte: len(head) + len(middle), Text: middle},
		{DocumentID: "doc1", UserID: "u1", Ord: 2, StartByte: len(head) + len(middle), EndByte: len(full), Text: tail},
	}
	embedder := retrieval.HashEmbedder{}
	memory, err := retrieval.NewMemoryChunks(context.Background(), embedder, chunks)
	if err != nil {
		t.Fatalf("memory chunks: %v", err)
	}

	app := stubRetrieverApp{stubDocuments{recs: map[string]*core.Record{
		"doc1": readableDocument("doc1", "u1", "Police", full),
	}}}
	r := &agentRetriever{
		app:    app,
		userID: "u1",
		chunks: memory,
		embedQuery: func(ctx context.Context, text string) ([]float32, error) {
			vectors, err := embedder.Embed(ctx, []string{text})
			if err != nil {
				return nil, err
			}
			return vectors[0], nil
		},
	}

	// A focus whose words appear nowhere in the document: term overlap finds
	// nothing, so without the chunk index the middle would be unreachable.
	req := ai.ReadRequest{IDs: []string{"doc1"}, Focus: "Selbstbehalt"}

	control, err := readUserDocuments(app, "u1", req, nil, focusExcerptBytes)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(control) != 1 || strings.Contains(control[0].Text, "150 EUR") {
		t.Fatalf("the fixture no longer isolates the dense ranking:\n%s", control[0].Text)
	}

	docs, err := r.read(context.Background(), req)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %#v", docs)
	}
	if !docs[0].Excerpted {
		t.Fatal("a focused read of a long document should report itself excerpted")
	}
	if !strings.Contains(docs[0].Text, "150 EUR") {
		t.Fatalf("the focused excerpt missed the passage it was asked for:\n%s", docs[0].Text)
	}
}

type fixedChunks []retrieval.ChunkHit

func (f fixedChunks) SearchChunks(_ context.Context, q retrieval.ChunkQuery) ([]retrieval.ChunkHit, error) {
	if len(q.DocumentIDs) == 0 {
		return f, nil
	}
	var kept fixedChunks
	for _, hit := range f {
		if slices.Contains(q.DocumentIDs, hit.DocumentID) {
			kept = append(kept, hit)
		}
	}
	return kept, nil
}

// neighbours scores "dense" at top and pads the list with background documents
// the way bge-m3 does, so the only thing deciding a standout is top.
func neighbours(top float64) fixedChunks {
	hits := fixedChunks{{DocumentID: "dense", Score: top}}
	for i, score := range []float64{0.347, 0.346, 0.341, 0.33, 0.32, 0.317, 0.31, 0.30, 0.29} {
		hits = append(hits, retrieval.ChunkHit{DocumentID: fmt.Sprintf("background%d", i), Score: score})
	}
	return hits
}

func TestFusedDocumentPageAddsDenseMatchesUnderTheListsFilters(t *testing.T) {
	r := hybridRetriever(t, nil)
	floor := similarityFloor("BAAI/bge-m3")
	page := func(q fulltext.Query) fulltext.Result {
		t.Helper()
		q.UserID = "u1"
		if q.Limit == 0 {
			q.Limit = 10
		}
		result, ok, err := r.fusedDocumentPage(context.Background(), q, floor)
		if err != nil || !ok {
			t.Fatalf("fused page: ok = %v, err = %v", ok, err)
		}
		return result
	}

	// "invoice" over an archive of German invoices: nothing stands out, and
	// every document at the floor answers it.
	r.chunks = fixedChunks{
		{DocumentID: "rechnung1", Score: 0.53}, {DocumentID: "rechnung2", Score: 0.51},
		{DocumentID: "rechnung3", Score: 0.49}, {DocumentID: "rechnung4", Score: 0.478},
	}
	if broad := page(fulltext.Query{Text: "invoice"}); broad.Total != 3 {
		t.Fatalf("want the three invoices at the floor: %#v", broad)
	}
	floor = similarityFloor("text-embedding-3-small")
	if unmeasured := page(fulltext.Query{Text: "invoice"}); unmeasured.Total != 0 {
		t.Fatalf("a model with no measured floor must fall back to standing out: %#v", unmeasured)
	}
	floor = similarityFloor("BAAI/bge-m3")

	r.chunks = neighbours(0.357)
	if unrelated := page(fulltext.Query{Text: "zebracrossing"}); unrelated.Total != 0 {
		t.Fatalf("nearest neighbours of an unrelated query were listed: %#v", unrelated)
	}

	r.chunks = neighbours(0.521)
	byMeaning := page(fulltext.Query{Text: "Versicherungspraemien"})
	if byMeaning.Total != 1 || byMeaning.Hits[0].ID != "dense" {
		t.Fatalf("want only the document that stands out: %#v", byMeaning)
	}

	both := page(fulltext.Query{Text: "insurance premium"})
	if both.Total != 2 || both.Hits[0].ID != "lexical" {
		t.Fatalf("the keyword match should lead the fused list of both: %#v", both)
	}
	second := page(fulltext.Query{Text: "insurance premium", Offset: 1, Limit: 1})
	if second.Total != 2 || len(second.Hits) != 1 || second.Hits[0].ID != both.Hits[1].ID {
		t.Fatalf("paging the fused list: %#v", second)
	}

	filtered := page(fulltext.Query{Text: "Versicherungspraemien", ProcessingStatus: "completed"})
	if filtered.Total != 0 || len(filtered.Hits) != 0 {
		t.Fatalf("a filter no document passes let the dense leg through: %#v", filtered)
	}
}

// A provider that is configured but down must cost the search box its
// matches by meaning, never its keyword matches or a keystroke's worth of wait.
func TestFusedDocumentPageKeepsKeywordsWhenTheProviderIsDown(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.embedQuery = func(ctx context.Context, _ string) ([]float32, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > maxDenseWait {
			t.Errorf("the query embedding is not bounded: deadline %v, set %v", deadline, ok)
		}
		return nil, io.ErrUnexpectedEOF
	}
	q := fulltext.Query{Text: "insurance premium", UserID: "u1", Limit: 10}
	result, ok, err := r.fusedDocumentPage(context.Background(), q, similarityFloor("BAAI/bge-m3"))
	if err != nil || !ok || result.Total != 1 || result.Hits[0].ID != "lexical" {
		t.Fatalf("keyword results were lost with the provider: ok = %v, err = %v, %#v", ok, err, result)
	}
}

// Both fixtures say "240 EUR"; "outsider" shares no word with the query and is
// the closest by meaning, so it is the hit that would jump the queue.
func TestFusedDocumentPageNeverRanksMeaningAboveKeywords(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.chunks = append(fixedChunks{{DocumentID: "outsider", Score: 0.60}, {DocumentID: "dense", Score: 0.55}}, neighbours(0.3)[1:]...)
	page := func(text string) fulltext.Result {
		t.Helper()
		result, ok, err := r.fusedDocumentPage(context.Background(),
			fulltext.Query{Text: text, UserID: "u1", Limit: 10}, similarityFloor("BAAI/bge-m3"))
		if err != nil || !ok {
			t.Fatalf("fused page: ok = %v, err = %v", ok, err)
		}
		return result
	}

	both := page("240 EUR")
	if both.Total != 3 || both.Hits[2].ID != "outsider" {
		t.Fatalf("a meaning-only hit must follow every keyword match: %#v", both)
	}

	// A strict miss widens to prefixes, as it does with embeddings off.
	if prefix := page("insur"); prefix.Total != 3 || prefix.Hits[0].ID != "lexical" {
		t.Fatalf("the prefix match should lead: %#v", prefix)
	}
}

// The chunk index knows readable, not owned, so a shared document close in
// meaning has to be kept off a mine-only list such as the Inbox here.
func TestFusedDocumentPageKeepsSharedDocumentsOffMine(t *testing.T) {
	r := hybridRetriever(t, nil)
	for id, owner := range map[string]string{"dense": "u1", "theirs": "u2"} {
		err := r.idx.Put(id, map[string]any{
			fulltext.FieldUser:  []string{"u1", owner},
			fulltext.FieldOwner: owner,
			fulltext.FieldTitle: id,
			fulltext.FieldAll:   id,
		})
		if err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	r.chunks = fixedChunks{{DocumentID: "theirs", Score: 0.60}, {DocumentID: "dense", Score: 0.55}}

	q := fulltext.Query{Text: "Versicherungspraemien", UserID: "u1", Owner: fulltext.OwnerMine, Limit: 10}
	result, ok, err := r.fusedDocumentPage(context.Background(), q, similarityFloor("BAAI/bge-m3"))
	if err != nil || !ok || result.Total != 1 || result.Hits[0].ID != "dense" {
		t.Fatalf("want only the caller's own document: ok = %v, err = %v, %#v", ok, err, result)
	}
}
