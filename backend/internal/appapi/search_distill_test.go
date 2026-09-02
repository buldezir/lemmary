package appapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
)

// fakeHelper answers every document with a note built from its id, records
// the batches it was given, and can be told to fail.
type fakeHelper struct {
	mu      sync.Mutex
	batches [][]string
	fail    bool
	// skip lists document ids the helper leaves out of its answer.
	skip map[string]bool
	// values are returned per document, for survey totals.
	values map[string]map[string]string
}

func (f *fakeHelper) Name() string  { return "fake" }
func (f *fakeHelper) Model() string { return "fake-model" }

func (f *fakeHelper) Distill(_ context.Context, req ai.DistillRequest) (ai.DistillResult, error) {
	ids := make([]string, 0, len(req.Docs))
	for _, d := range req.Docs {
		ids = append(ids, d.ID)
	}
	f.mu.Lock()
	f.batches = append(f.batches, ids)
	f.mu.Unlock()
	if f.fail {
		return ai.DistillResult{}, errors.New("helper down")
	}
	rows := make([]ai.DistillRow, 0, len(req.Docs))
	for _, d := range req.Docs {
		if f.skip[d.ID] {
			continue
		}
		row := ai.DistillRow{
			ID:       d.ID,
			Relevant: !strings.HasPrefix(d.ID, "irrelevant"),
			Notes:    "note for " + d.ID + " about " + req.Question,
			Quotes:   []string{"quote from " + d.ID},
			Values:   f.values[d.ID],
		}
		rows = append(rows, row)
	}
	return ai.DistillResult{Rows: rows, Usage: ai.Usage{Prompt: 10, Completion: 2}}, nil
}

func distillApp(docs map[string]string) stubRetrieverApp {
	recs := map[string]*core.Record{}
	for id, text := range docs {
		recs[id] = readableDocument(id, "me", "Doc "+id, text)
	}
	return stubRetrieverApp{stubDocuments{recs: recs}}
}

func TestReadPassesASmallReadThroughAsText(t *testing.T) {
	helper := &fakeHelper{}
	r := &agentRetriever{app: distillApp(map[string]string{"a": "short text a", "b": "short text b"}), userID: "me", helper: helper}

	got, err := r.read(context.Background(), ai.ReadRequest{IDs: []string{"a", "b"}, Question: "q"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, doc := range got {
		if doc.Distilled || doc.Text == "" {
			t.Fatalf("a small read should pass through raw: %+v", doc)
		}
	}
	if len(helper.batches) != 0 {
		t.Fatalf("the helper was called for a small read: %v", helper.batches)
	}
}

func TestReadDistillsManyDocuments(t *testing.T) {
	helper := &fakeHelper{}
	docs := map[string]string{}
	ids := make([]string, 0, distillMinDocs+1)
	for i := 0; i <= distillMinDocs; i++ {
		id := fmt.Sprintf("d%d", i)
		docs[id] = "text of " + id
		ids = append(ids, id)
	}
	r := &agentRetriever{app: distillApp(docs), userID: "me", helper: helper}

	got, err := r.read(context.Background(), ai.ReadRequest{IDs: ids, Focus: "rent"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("got %d documents, want %d", len(got), len(ids))
	}
	for _, doc := range got {
		if !doc.Distilled || doc.Text != "" || !strings.Contains(doc.Notes, "about rent") || len(doc.Quotes) != 1 {
			t.Fatalf("expected a distilled document with notes and a quote, got %+v", doc)
		}
		if !doc.Relevant {
			t.Fatalf("the helper said relevant: %+v", doc)
		}
	}
	// Short documents travel together: one helper call, not one per document.
	if len(helper.batches) != 1 || len(helper.batches[0]) != len(ids) {
		t.Fatalf("batches = %v, want one batch of %d", helper.batches, len(ids))
	}
}

func TestReadDistillsALargeReadAndShowsTheHelperTheWholeText(t *testing.T) {
	helper := &fakeHelper{}
	long := "Kopf. " + strings.Repeat("Absatz über nichts. ", 2000) + " Die Kaltmiete beträgt 900 EUR. Schluss."
	if len(long) <= distillThresholdBytes {
		t.Fatalf("fixture of %d bytes is under the distil threshold", len(long))
	}
	r := &agentRetriever{app: distillApp(map[string]string{"lease": long}), userID: "me", helper: helper}

	got, err := r.read(context.Background(), ai.ReadRequest{IDs: []string{"lease"}, Question: "Wie hoch ist die Kaltmiete?"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || !got[0].Distilled || got[0].Text != "" {
		t.Fatalf("a read past the threshold should be distilled: %+v", got)
	}
	if !strings.Contains(got[0].Notes, "Wie hoch ist die Kaltmiete?") {
		t.Fatalf("the user's question should be what the helper read for: %+v", got[0])
	}
}

func TestReadFallsBackToExcerptsWhenTheHelperFails(t *testing.T) {
	helper := &fakeHelper{fail: true}
	long := "Kopf. " + strings.Repeat("Absatz über nichts. ", 2000) + " Die Kaltmiete beträgt 900 EUR. Schluss."
	r := &agentRetriever{app: distillApp(map[string]string{"lease": long}), userID: "me", helper: helper}

	got, err := r.read(context.Background(), ai.ReadRequest{IDs: []string{"lease"}, Focus: "Kaltmiete"})
	if err != nil {
		t.Fatalf("a helper failure must not fail the read: %v", err)
	}
	doc := got[0]
	if doc.Distilled || doc.Text == "" {
		t.Fatalf("expected text on fallback, got %+v", doc)
	}
	if !doc.Excerpted || len(doc.Text) > focusExcerptBytes {
		t.Fatalf("fallback text should be the agent's own excerpt size, got %d bytes excerpted=%v", len(doc.Text), doc.Excerpted)
	}
	if !strings.Contains(doc.Text, "900 EUR") {
		t.Fatalf("fallback excerpt lost the focus: %s", doc.Text)
	}
}

func TestReadKeepsTextForDocumentsTheHelperSkipped(t *testing.T) {
	helper := &fakeHelper{skip: map[string]bool{"d3": true}}
	docs := map[string]string{}
	ids := []string{}
	for i := 0; i <= distillMinDocs; i++ {
		id := fmt.Sprintf("d%d", i)
		docs[id] = "text of " + id
		ids = append(ids, id)
	}
	r := &agentRetriever{app: distillApp(docs), userID: "me", helper: helper}
	got, err := r.read(context.Background(), ai.ReadRequest{IDs: ids, Question: "q"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, doc := range got {
		if doc.ID == "d3" {
			if doc.Distilled || doc.Text != "text of d3" {
				t.Fatalf("a skipped document should keep its text: %+v", doc)
			}
			continue
		}
		if !doc.Distilled {
			t.Fatalf("%s should be distilled: %+v", doc.ID, doc)
		}
	}
}

func TestPackDistillBatchesRespectsTheBudget(t *testing.T) {
	docs := []ai.DistillDoc{
		{ID: "a", Text: strings.Repeat("x", 60)},
		{ID: "b", Text: strings.Repeat("x", 50)},
		{ID: "c", Text: strings.Repeat("x", 150)},
		{ID: "d", Text: strings.Repeat("x", 10)},
	}
	batches := packDistillBatches(docs, 100)
	want := [][]string{{"a"}, {"b"}, {"c"}, {"d"}}
	if len(batches) != len(want) {
		t.Fatalf("batches = %d, want %d", len(batches), len(want))
	}
	for i, batch := range batches {
		if len(batch) != len(want[i]) || batch[0].ID != want[i][0] {
			t.Fatalf("batch %d = %+v, want %v", i, batch, want[i])
		}
	}

	together := packDistillBatches(docs[:2], 200)
	if len(together) != 1 || len(together[0]) != 2 {
		t.Fatalf("two documents under the budget should share a batch: %+v", together)
	}
}
