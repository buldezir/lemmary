package worker

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/retrieval"
	"lemmary/backend/internal/testpb"
)

func linkTestDocument(t *testing.T, app core.App, userID, title string, refs ...string) *core.Record {
	t.Helper()
	record, err := app.FindRecordById("documents", makeImageDocument(t, app, userID, title))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) > 0 {
		record.Set("reference_numbers", normalizeReferences(refs))
		if err := app.Save(record); err != nil {
			t.Fatalf("save reference numbers: %v", err)
		}
	}
	return record
}

func runLinkRelated(t *testing.T, app core.App, step *LinkRelatedStep, doc *core.Record) []string {
	t.Helper()
	state := &StepState{App: app, Document: doc, Logger: slog.Default()}
	if skip, err := step.ShouldSkip(state); err != nil || skip {
		t.Fatalf("ShouldSkip = %v, %v; want it to run", skip, err)
	}
	if err := step.Run(context.Background(), state); err != nil {
		t.Fatalf("Run: %v", err)
	}
	stored, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	return stored.GetStringSlice("related")
}

func TestNormalizeReferences(t *testing.T) {
	t.Parallel()
	got := normalizeReferences([]string{"INV-2024/0042", "inv 2024 0042", "Rechnung", "A-12", "  ", "Vertrag Nr. 7781-B"})
	want := []string{"INV20240042", "VERTRAGNR7781B"}
	if !slices.Equal(got, want) {
		t.Fatalf("normalizeReferences = %v, want %v", got, want)
	}
}

func TestDocumentRelationsMustBeOwnedAndNotSelf(t *testing.T) {
	app := testpb.Open(t)
	owner := makeUserForDrain(t, app, "owner@example.com")
	other := makeUserForDrain(t, app, "other@example.com")
	doc := linkTestDocument(t, app, owner, "invoice")
	mine := linkTestDocument(t, app, owner, "receipt")
	theirs := linkTestDocument(t, app, other, "their receipt")

	for _, tc := range []struct {
		name    string
		related []string
		ok      bool
	}{
		{"own document", []string{mine.Id}, true},
		{"another user's document", []string{theirs.Id}, false},
		{"itself", []string{doc.Id}, false},
	} {
		doc.Set("related", tc.related)
		if err := validateDocumentRelationOwnership(app, doc); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok = %v", tc.name, err, tc.ok)
		}
	}
}

func TestLinkRelatedLinksTheOwnersDocumentsSharingAReference(t *testing.T) {
	app := testpb.Open(t)
	owner := makeUserForDrain(t, app, "owner@example.com")
	other := makeUserForDrain(t, app, "other@example.com")
	invoice := linkTestDocument(t, app, owner, "invoice", "INV-2024/0042")
	linkTestDocument(t, app, other, "someone else's", "INV-2024/0042")
	linkTestDocument(t, app, owner, "unrelated", "ORD-9917")
	receipt := linkTestDocument(t, app, owner, "receipt", "inv 2024 0042")

	got := runLinkRelated(t, app, &LinkRelatedStep{RelatedLinking{Enabled: true}}, receipt)
	if !slices.Equal(got, []string{invoice.Id}) {
		t.Fatalf("related = %v, want only the owner's invoice %s", got, invoice.Id)
	}
}

// A court case runs to dozens of filings; every one of them carries the case
// number, and all of them belong together.
func TestLinkRelatedLinksEveryDocumentOfALargeMatter(t *testing.T) {
	app := testpb.Open(t)
	owner := makeUserForDrain(t, app, "owner@example.com")
	var filings []string
	for i := range 15 {
		filings = append(filings, linkTestDocument(t, app, owner, fmt.Sprintf("filing %d", i), "20 F 415/26").Id)
	}
	doc := linkTestDocument(t, app, owner, "another filing", "20 F 415/26")

	got := runLinkRelated(t, app, &LinkRelatedStep{RelatedLinking{Enabled: true}}, doc)
	slices.Sort(got)
	slices.Sort(filings)
	if !slices.Equal(got, filings) {
		t.Fatalf("related = %v, want all %d earlier filings", got, len(filings))
	}
}

func TestLinkRelatedKeepsExistingLinksInEitherDirection(t *testing.T) {
	app := testpb.Open(t)
	owner := makeUserForDrain(t, app, "owner@example.com")
	manual := linkTestDocument(t, app, owner, "contract")
	invoice := linkTestDocument(t, app, owner, "invoice", "INV-2024/0042")
	invoice.Set("related", []string{manual.Id})
	reminder := linkTestDocument(t, app, owner, "reminder", "INV-2024/0042")
	reminder.Set("related", []string{invoice.Id})
	for _, record := range []*core.Record{invoice, reminder} {
		if err := app.Save(record); err != nil {
			t.Fatal(err)
		}
	}

	got := runLinkRelated(t, app, &LinkRelatedStep{RelatedLinking{Enabled: true}}, invoice)
	if !slices.Equal(got, []string{manual.Id}) {
		t.Fatalf("related = %v, want the manual link kept and the reminder's link not repeated", got)
	}
}

// Two long documents on the same matter: their averages match, while each
// chunk of one scores only ~0.71 against the other's average. Scoring averages
// against single chunks never linked them.
func TestLinkRelatedBySimilarityOfTheDocumentsAverages(t *testing.T) {
	app := testpb.Open(t)
	owner := makeUserForDrain(t, app, "owner@example.com")
	doc := linkTestDocument(t, app, owner, "statement")
	near := linkTestDocument(t, app, owner, "reply to the statement")
	far := linkTestDocument(t, app, owner, "dentist bill")

	vectors := map[string][][]float32{
		doc.Id:  {{1, 0}, {0, 1}},
		near.Id: {{0, 1}, {1, 0}},
		far.Id:  {{1, 0.1}, {0.9, 0}},
	}
	index := &retrieval.MemoryChunks{}
	for id, chunkVectors := range vectors {
		chunks := make([]embedstore.Chunk, 0, len(chunkVectors))
		for i, vector := range chunkVectors {
			chunks = append(chunks, embedstore.Chunk{DocumentID: id, Ordinal: i, UserID: owner, Model: "m", Dims: 2, Vector: vector})
			index.Chunks = append(index.Chunks, retrieval.MemoryChunk{DocumentID: id, Ord: i, UserID: owner, Vector: vector})
		}
		state := embedstore.State{DocumentID: id, UserID: owner, Model: "m", Dims: 2, Status: embedstore.StatusOK}
		if err := embedstore.Replace(app.DB(), state, chunks); err != nil {
			t.Fatalf("store chunks: %v", err)
		}
	}

	step := &LinkRelatedStep{RelatedLinking{Enabled: true, Threshold: 0.9, Chunks: index}}
	if got := runLinkRelated(t, app, step, doc); !slices.Equal(got, []string{near.Id}) {
		t.Fatalf("related = %v, want only %s, whose average matches", got, near.Id)
	}
}

// Off has to look like a skipped step, as with embed.
func TestLinkRelatedSkipsWhenOffOrADuplicate(t *testing.T) {
	app := testpb.Open(t)
	owner := makeUserForDrain(t, app, "owner@example.com")
	original := linkTestDocument(t, app, owner, "original")
	duplicate := linkTestDocument(t, app, owner, "copy")
	duplicate.Set("duplicate_of", original.Id)

	for name, tc := range map[string]struct {
		step *LinkRelatedStep
		doc  *core.Record
	}{
		"off":         {&LinkRelatedStep{}, original},
		"a duplicate": {&LinkRelatedStep{RelatedLinking{Enabled: true}}, duplicate},
	} {
		skip, err := tc.step.ShouldSkip(&StepState{Document: tc.doc})
		if err != nil || !skip {
			t.Errorf("%s: ShouldSkip = %v, %v; want a skip", name, skip, err)
		}
	}
}
