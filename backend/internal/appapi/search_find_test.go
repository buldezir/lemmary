package appapi

import (
	"context"
	"strings"
	"testing"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chunk"
)

func TestFindScreensEveryCandidateThenReadsTheSurvivors(t *testing.T) {
	r := hybridRetriever(t, nil)
	helper := &fakeHelper{
		verdicts: map[string]string{"dense": ai.VerdictNo},
		chunks:   map[string][]int{"lexical": {0}},
	}
	r.helper = helper

	var progress []string
	result, err := r.find(context.Background(), ai.FindArgs{
		Query:    "insurance premium",
		Question: "What is the yearly premium?",
	}, func(phase string, done, total int) {
		progress = append(progress, phase)
	})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if result.Candidates != 2 || result.Screened != 2 {
		t.Fatalf("every completed candidate should be screened: %+v", result)
	}
	if len(helper.screened) != 1 || len(helper.screened[0]) != 2 {
		t.Fatalf("screen batches = %+v", helper.screened)
	}
	for _, doc := range helper.screened[0] {
		if doc.Title == "" || len(doc.Passages) == 0 {
			t.Fatalf("the screen should see the catalogue entry and the matched passages: %+v", doc)
		}
	}
	// The no verdict keeps "dense" out of the read; "lexical" was not
	// mentioned and is kept as maybe.
	if result.Read != 1 || len(helper.batches) != 1 || helper.batches[0][0] != "lexical" {
		t.Fatalf("read pass = %d docs, batches %v", result.Read, helper.batches)
	}
	if !strings.Contains(helper.inputs["lexical"], "[chunk 0]") {
		t.Fatalf("the read pass should show chunk markers: %q", helper.inputs["lexical"])
	}
	if len(result.Documents) != 1 || result.Documents[0].ID != "lexical" || len(result.Documents[0].Chunks) != 1 || result.Documents[0].ChunkCount != 1 {
		t.Fatalf("documents = %+v", result.Documents)
	}
	if result.Documents[0].Notes == "" || len(result.Documents[0].Quotes) == 0 {
		t.Fatalf("a found document carries the helper's notes and quotes: %+v", result.Documents[0])
	}
	if len(result.Hits) != 1 || result.Hits[0].ID != "lexical" {
		t.Fatalf("hits = %+v", result.Hits)
	}
	if strings.Join(progress, ",") != "screen,screen,read,read" {
		t.Fatalf("progress = %v", progress)
	}
}

func TestFindKeepsEveryCandidateWhenTheScreenFails(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.helper = &fakeHelper{screenFail: true}
	result, err := r.find(context.Background(), ai.FindArgs{Query: "insurance premium"}, nil)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if result.Read != 2 || len(result.Documents) != 2 {
		t.Fatalf("a failed screen must not drop documents: %+v", result)
	}
}

func TestFindDropsIrrelevantReadsAndReportsUnresolvedFilters(t *testing.T) {
	r := hybridRetriever(t, nil)
	r.helper = &fakeHelper{skip: map[string]bool{"dense": true}}
	result, err := r.find(context.Background(), ai.FindArgs{Query: "insurance premium"}, nil)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if result.Read != 2 || len(result.Documents) != 1 || result.Documents[0].ID != "lexical" {
		t.Fatalf("a document the reader did not answer for is not found: %+v", result)
	}

	result, err = r.find(context.Background(), ai.FindArgs{Query: "premium", Tags: []string{"nonexistent"}}, nil)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(result.Unresolved) != 1 || result.Screened != 0 {
		t.Fatalf("an unknown tag should resolve nothing and say so: %+v", result)
	}
}

func TestFindShowsALongCandidateAsItsBestChunks(t *testing.T) {
	r := hybridRetriever(t, nil)
	long := longText(400)
	needle := "The home insurance premium is 240 EUR per year, the letter says."
	long = long[:len(long)/2] + needle + long[len(long)/2:]
	app := r.app.(stubRetrieverApp)
	app.recs["lexical"] = readableDocument("lexical", "u1", "Insurance letter", long)
	// The in-memory chunk store was embedded for the short text; a stale
	// dense ranking is not what this test is about, so keywords rank here.
	r.chunks = nil
	helper := &fakeHelper{}
	r.helper = helper

	if _, err := r.find(context.Background(), ai.FindArgs{Query: "insurance premium"}, nil); err != nil {
		t.Fatalf("find: %v", err)
	}
	shown := helper.inputs["lexical"]
	if len(shown) > findExcerptBytes+500 {
		t.Fatalf("the reader was shown %d bytes, want about %d", len(shown), findExcerptBytes)
	}
	if !strings.Contains(shown, needle) || !strings.Contains(shown, "[chunk ") {
		t.Fatalf("the excerpt should carry the matching chunk with its marker: %q", shown[:min(len(shown), 200)])
	}
}

func TestReadExactServesChunksAndBoundedFullReads(t *testing.T) {
	text := longText(80)
	chunks, _ := chunk.Split(text, chunk.DefaultOptions())
	app := distillApp(map[string]string{"doc": text, "short": "tiny"})

	docs, err := readExact(app, "me", ai.ReadRequest{IDs: []string{"doc"}, Chunks: []int{2, 3, 99}})
	if err != nil || len(docs) != 1 {
		t.Fatalf("chunk read: %v %+v", err, docs)
	}
	if !strings.HasPrefix(docs[0].Text, "[chunk 2-3]\n") || !strings.Contains(docs[0].Text, text[chunks[2].Start:chunks[3].End]) {
		t.Fatalf("chunk text = %q", docs[0].Text[:40])
	}
	if docs[0].ChunkCount != len(chunks) || len(docs[0].Chunks) != 2 || len(docs[0].UnknownChunks) != 1 || docs[0].UnknownChunks[0] != 99 || !docs[0].Excerpted {
		t.Fatalf("chunk read metadata = %+v", docs[0])
	}

	if _, err := readExact(app, "me", ai.ReadRequest{IDs: []string{"doc"}, Chunks: []int{500}}); err == nil {
		t.Fatal("chunks past the end should be an error")
	}

	docs, err = readExact(app, "me", ai.ReadRequest{IDs: []string{"doc"}, Full: true, MaxBytes: len(text) + 1})
	if err != nil || len(docs) != 1 || docs[0].Text != text || docs[0].Excerpted || docs[0].ChunkCount != len(chunks) {
		t.Fatalf("full read: %v %+v", err, docs)
	}
	if _, err := readExact(app, "me", ai.ReadRequest{IDs: []string{"doc"}, Full: true, MaxBytes: len(text) - 1}); err == nil || !strings.Contains(err.Error(), "read_chunks") {
		t.Fatalf("an oversized full read should be refused with a hint: %v", err)
	}
	if _, err := readExact(app, "me", ai.ReadRequest{IDs: []string{"doc", "short"}, Full: true, MaxBytes: 1 << 20}); err == nil {
		t.Fatal("full reads one document at a time")
	}

	docs, err = readExact(app, "someone-else", ai.ReadRequest{IDs: []string{"doc"}, Full: true, MaxBytes: 1 << 20})
	if err != nil || len(docs) != 0 {
		t.Fatalf("another owner's document must not be readable: %v %+v", err, docs)
	}
}

func TestReadThroughTheRetrieverRoutesExactReadsPastTheHelper(t *testing.T) {
	r := hybridRetriever(t, nil)
	helper := &fakeHelper{}
	r.helper = helper
	docs, err := r.read(context.Background(), ai.ReadRequest{IDs: []string{"lexical"}, Chunks: []int{0}})
	if err != nil || len(docs) != 1 || docs[0].Distilled || !strings.Contains(docs[0].Text, lexicalText) {
		t.Fatalf("chunk read via retriever: %v %+v", err, docs)
	}
	docs, err = r.read(context.Background(), ai.ReadRequest{IDs: []string{"lexical"}, Full: true, MaxBytes: 1 << 20})
	if err != nil || len(docs) != 1 || docs[0].Text != lexicalText {
		t.Fatalf("full read via retriever: %v %+v", err, docs)
	}
	if len(helper.batches) != 0 {
		t.Fatal("exact reads must not reach the helper")
	}
}

func TestDistilledReadsCarryTheHelpersChunks(t *testing.T) {
	texts := map[string]string{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		texts[id] = "Text of " + id + "."
	}
	r := &agentRetriever{app: distillApp(texts), userID: "me", helper: &fakeHelper{chunks: map[string][]int{"a": {0}}}}
	ids := []string{"a", "b", "c", "d", "e", "f"}
	docs, err := r.read(context.Background(), ai.ReadRequest{IDs: ids, Question: "q"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, doc := range docs {
		if !doc.Distilled {
			t.Fatalf("six documents should be distilled: %+v", doc)
		}
		if doc.ID == "a" && (len(doc.Chunks) != 1 || doc.ChunkCount != 1) {
			t.Fatalf("distilled content should carry the chunk pointers: %+v", doc)
		}
	}
}
