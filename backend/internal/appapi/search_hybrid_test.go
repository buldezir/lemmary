package appapi

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/retrieval"
)

// stubRetrieverApp is stubDocuments plus the two things the search path needs
// beyond a record lookup: somewhere to log, and a filter query that finds
// nothing (these tests pass no named-entity filters).
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

// The two documents of the hybrid fixture. The lexical one repeats the query's
// words; the dense one says the same thing as a compound the keyword index
// cannot reach from the query, which is the case the whole feature exists for.
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

// TestSearchFusesTheDenseListIntoTheLexicalOne uses a query no keyword search
// can answer -- a compound in the wrong spelling, two edits from anything in
// the archive -- and proves the point with the control: the same search with
// the chunk index unplugged finds nothing at all.
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

// TestSearchDegradesToLexicalWhenEmbeddingFails is the promise the whole dense
// path is written around: a retrieval tool that fails because the vector store
// is unhappy is worse than one that answers from keywords.
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

// TestReadFocusRanksWithTheChunkIndex checks the other half of the wiring: a
// focused read of a long document uses the stored chunks to decide what to
// show, so the answer in the middle survives the excerpt.
func TestReadFocusRanksWithTheChunkIndex(t *testing.T) {
	// Long enough that one excerpt cannot hold it, and with the answer far
	// enough in that the head-and-first-windows excerpt cannot reach it.
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

	// A focus whose words appear nowhere in the document: term overlap over
	// windows cut from the text finds nothing, so the excerpt would be the head
	// and the tail, and the sentence in the middle would be unreachable.
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
