package retrieval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"lemmary/backend/internal/retrieval/testdata"
)

// This is the fusion half of the retrieval evaluation. The Bleve half lives in
// internal/fulltext and measures the real index; this one measures what fusing
// a dense list into a lexical one does to the same corpus and the same queries,
// with no provider, no API key and no vector index.
//
// The dense signal here is HashEmbedder, which is not a semantic model: it
// hashes words and their character n-grams. So it cannot answer a true
// cross-language question, and the numbers below are not a prediction of what a
// real embedding model scores. What it does model faithfully is the shape of
// the pipeline — a chunk-level list, grouped to documents, fused by rank — and
// the classes an n-gram signal genuinely reaches: inflections, compounds and
// typos, where the lexical index needs the exact token and this does not.
//
// Measured at calibration, over the same 23 cases the Bleve evaluation uses:
//
//	lexical: recall@5 0.641  MRR 0.652
//	hybrid:  recall@5 0.957  MRR 0.928
//
// Improved by fusion: 3 morphology, 2 typo, 2 paraphrase, 1 filter. The floors
// sit under those numbers by enough to absorb a reordering and not enough to
// hide a regression. Never lower one to make a change pass.
const (
	hybridRecallFloor = 0.92
	hybridMRRFloor    = 0.89
)

const evalK = 5

// chunkCorpus cuts every document into a metadata header chunk plus body
// chunks, which is how the ingestion pipeline will chunk them.
func chunkCorpus() []MemoryChunk {
	chunks := make([]MemoryChunk, 0, 128)
	for _, doc := range testdata.Documents() {
		header := strings.Join([]string{
			doc.Title, doc.TitleOriginal, doc.Purpose, doc.Summary,
			doc.DocumentType, doc.Correspondent, strings.Join(doc.Tags, " "),
		}, "\n")
		chunks = append(chunks, MemoryChunk{
			DocumentID: doc.ID,
			UserID:     doc.User,
			Ord:        0,
			Text:       header,
		})
		for _, w := range Windows(doc.Text, nil) {
			chunks = append(chunks, MemoryChunk{
				DocumentID: doc.ID,
				UserID:     doc.User,
				Ord:        w.Ord + 1,
				StartByte:  w.StartByte,
				EndByte:    w.EndByte,
				Text:       doc.Text[w.StartByte:w.EndByte],
			})
		}
	}
	return chunks
}

// lexicalRanking is the baseline the fusion is measured against: whole-token
// matching over each document's searchable text, admitted by the same
// min-should-match rule the relaxed index query uses.
//
// Tokens, not substrings, and that is the point. A term index can only match
// the words that are there, which is why "Vollkasko" misses
// "Vollkaskoversicherung" and a paraphrase misses everything. Scoring the
// baseline with TermOverlap instead would quietly hand it substring matching
// and hide the gap fusion exists to close.
func lexicalRanking(query string, filters testdata.Filters) []Ranked {
	terms := focusTerms(query)
	if len(terms) == 0 {
		return nil
	}
	need := evalMinShouldMatch(len(terms))

	scored := make([]Ranked, 0, 16)
	for _, doc := range testdata.Documents() {
		if doc.User != "u1" || !matchesFilters(doc, filters) {
			continue
		}
		all := strings.Join([]string{
			doc.Title, doc.TitleOriginal, doc.Purpose, doc.Summary,
			doc.DocumentType, doc.Correspondent, strings.Join(doc.Tags, " "), doc.Text,
		}, "\n")
		tokens := map[string]int{}
		for _, token := range focusTerms(all) {
			tokens[token]++
		}
		matched := 0
		for _, term := range terms {
			if tokens[term] > 0 {
				matched++
			}
		}
		if matched < need {
			continue
		}
		scored = append(scored, Ranked{ID: doc.ID, Score: float64(matched)})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].ID < scored[j].ID
	})
	return scored
}

// evalMinShouldMatch mirrors the index's min-should-match rule. Duplicated
// rather than imported: this package must not depend on the index, and the
// baseline is only a reference point, not the thing under test.
func evalMinShouldMatch(n int) int {
	switch {
	case n <= 2:
		return n
	case n <= 5:
		return n - 1
	default:
		return int(math.Ceil(0.7 * float64(n)))
	}
}

func matchesFilters(doc testdata.Document, f testdata.Filters) bool {
	if f.DocumentType != "" && doc.DocumentType != f.DocumentType {
		return false
	}
	if f.Correspondent != "" && doc.Correspondent != f.Correspondent {
		return false
	}
	if f.DateFrom != "" && doc.Date.Format("2006-01-02") < f.DateFrom {
		return false
	}
	if f.DateTo != "" && doc.Date.Format("2006-01-02") > f.DateTo {
		return false
	}
	for _, want := range f.Tags {
		found := false
		for _, tag := range doc.Tags {
			if tag == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// eligibleIDs is the pre-filter the real retriever applies: filters are
// resolved against the document index, and the chunk search is restricted to
// what came back.
func eligibleIDs(filters testdata.Filters) []string {
	ids := make([]string, 0, 16)
	for _, doc := range testdata.Documents() {
		if doc.User == "u1" && matchesFilters(doc, filters) {
			ids = append(ids, doc.ID)
		}
	}
	return ids
}

func evalScore(ranked []Ranked, want []string) (recall, rr float64) {
	ids := IDs(ranked)
	if len(ids) > evalK {
		ids = ids[:evalK]
	}
	found, best := 0, 0
	for _, id := range want {
		for rank, got := range ids {
			if got != id {
				continue
			}
			found++
			if best == 0 || rank+1 < best {
				best = rank + 1
			}
			break
		}
	}
	recall = float64(found) / float64(len(want))
	if best > 0 {
		rr = 1 / float64(best)
	}
	return recall, rr
}

func TestHybridEvalBeatsLexical(t *testing.T) {
	ctx := context.Background()
	embedder := HashEmbedder{}
	chunks, err := NewMemoryChunks(ctx, embedder, chunkCorpus())
	if err != nil {
		t.Fatalf("NewMemoryChunks: %v", err)
	}

	var lexRecall, lexMRR, hybRecall, hybMRR float64
	improved := map[testdata.Kind]int{}
	cases := testdata.Cases()

	for _, c := range cases {
		lexical := lexicalRanking(c.Query, c.Filters)

		vectors, err := embedder.Embed(ctx, []string{c.Query})
		if err != nil {
			t.Fatalf("Embed: %v", err)
		}
		chunkHits, err := chunks.SearchChunks(ctx, ChunkQuery{
			Vector:      vectors[0],
			UserID:      "u1",
			DocumentIDs: eligibleIDs(c.Filters),
			K:           4 * evalK,
		})
		if err != nil {
			t.Fatalf("SearchChunks: %v", err)
		}
		dense, _ := GroupChunks(chunkHits, MaxPassagesPerDocument)
		hybrid := RRF(lexical, dense)

		lr, lm := evalScore(lexical, c.Want)
		hr, hm := evalScore(hybrid, c.Want)
		lexRecall += lr
		lexMRR += lm
		hybRecall += hr
		hybMRR += hm
		if hr > lr {
			improved[c.Kind]++
		}
		if hr < lr {
			t.Errorf("%q: fusing dense cost recall, %.2f -> %.2f", c.Name, lr, hr)
		}
	}

	n := float64(len(cases))
	lexRecall, lexMRR = lexRecall/n, lexMRR/n
	hybRecall, hybMRR = hybRecall/n, hybMRR/n
	t.Logf("lexical: recall@%d %.3f  MRR %.3f", evalK, lexRecall, lexMRR)
	t.Logf("hybrid:  recall@%d %.3f  MRR %.3f", evalK, hybRecall, hybMRR)
	t.Logf("improved by fusion: %v", improved)

	if hybRecall < lexRecall {
		t.Errorf("hybrid recall %.3f below lexical %.3f", hybRecall, lexRecall)
	}
	if hybRecall < hybridRecallFloor {
		t.Errorf("hybrid recall@%d %.3f below the %.2f floor", evalK, hybRecall, hybridRecallFloor)
	}
	if hybMRR < hybridMRRFloor {
		t.Errorf("hybrid MRR %.3f below the %.2f floor", hybMRR, hybridMRRFloor)
	}

	// The classes an n-gram signal can reach are exactly the ones a term index
	// cannot: a different inflection, a compound, a word spelled wrong.
	if improved[testdata.KindMorphology] == 0 {
		t.Error("fusion answered no morphology case")
	}
	if improved[testdata.KindParaphrase]+improved[testdata.KindTypo] == 0 {
		t.Error("fusion answered no paraphrase and no typo")
	}
}

// The dense leg is user-scoped in the query, not filtered afterwards: the
// corpus holds another account's copy of one document, worded the same.
func TestHybridEvalKeepsOwnerScoping(t *testing.T) {
	ctx := context.Background()
	embedder := HashEmbedder{}
	chunks, err := NewMemoryChunks(ctx, embedder, chunkCorpus())
	if err != nil {
		t.Fatalf("NewMemoryChunks: %v", err)
	}
	vectors, err := embedder.Embed(ctx, []string{"Rechnung Badezimmer Steigleitung"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	hits, err := chunks.SearchChunks(ctx, ChunkQuery{Vector: vectors[0], UserID: "u1", K: 50})
	if err != nil {
		t.Fatalf("SearchChunks: %v", err)
	}
	for _, hit := range hits {
		if hit.DocumentID == "other-owner" {
			t.Fatal("the dense leg returned another account's document")
		}
	}
	if len(hits) == 0 {
		t.Fatal("the dense leg returned nothing at all")
	}
}

func TestHashEmbedderIsDeterministicAndNormalised(t *testing.T) {
	embedder := HashEmbedder{Dim: 64}
	got, err := embedder.Embed(context.Background(), []string{
		"Kaltmiete und Betriebskosten",
		"Kaltmiete und Betriebskosten",
		"vaccination record booster",
		"",
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 4 || len(got[0]) != 64 {
		t.Fatalf("got %d vectors of %d dims", len(got), len(got[0]))
	}
	if fmt.Sprint(got[0]) != fmt.Sprint(got[1]) {
		t.Fatal("the same text embedded differently twice")
	}
	if score := Cosine(got[0], got[1]); score < 0.999 {
		t.Fatalf("identical texts scored %v", score)
	}
	if score := Cosine(got[0], got[2]); score > 0.5 {
		t.Fatalf("unrelated texts scored %v", score)
	}
	// A related word scores above an unrelated one, which is the whole reason
	// the n-grams are there.
	related, _ := embedder.Embed(context.Background(), []string{"Kaltmieten"})
	if Cosine(got[0], related[0]) <= Cosine(got[2], related[0]) {
		t.Fatal("an inflected form did not score above an unrelated text")
	}
	// A blank input is a zero vector, not a panic, and scores nothing.
	if Cosine(got[3], got[0]) != 0 {
		t.Fatal("an empty text should not be similar to anything")
	}
	// A dims mismatch scores 0 rather than panicking.
	if Cosine(got[0], []float32{1, 2}) != 0 {
		t.Fatal("mismatched dimensions should score 0")
	}
}
