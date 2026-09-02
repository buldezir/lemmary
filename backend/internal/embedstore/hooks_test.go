package embedstore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// fakeFinder answers the one question the entity fan-out asks: which documents
// reference this record. It records the filters it was given, because the
// filter is half of what the fan-out has to get right -- a relation list needs
// `?=`, and an `=` against it silently matches nothing.
type fakeFinder struct {
	ids     []string
	filters []string
	err     error
}

func (f *fakeFinder) FindRecordsByFilter(
	_ any, filter, _ string, limit, offset int, _ ...dbx.Params,
) ([]*core.Record, error) {
	f.filters = append(f.filters, filter)
	if f.err != nil {
		return nil, f.err
	}
	collection := core.NewBaseCollection("documents")
	out := []*core.Record{}
	for i := offset; i < len(f.ids) && len(out) < limit; i++ {
		record := core.NewRecord(collection)
		record.Id = f.ids[i]
		out = append(out, record)
	}
	return out, nil
}

func staleFlags(t *testing.T, db *dbx.DB, ids ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, id := range ids {
		state, ok, err := Get(db, id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		out[id] = ok && state.Stale
	}
	return out
}

// The header passage embeds the resolved names of a document's tag, type and
// correspondent, so renaming one of them dates every vector that quotes it.
// Nothing on the documents collection fires for that rename, which is why the
// fan-out exists at all.
func TestMarkStaleForEntityDatesEveryReferencingDocument(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	for _, id := range []string{"doc-tagged", "doc-also-tagged", "doc-untagged"} {
		insertDocument(t, db, id, "text")
		if err := Replace(db, sampleState(id), sampleChunks(id)); err != nil {
			t.Fatalf("Replace %s: %v", id, err)
		}
	}

	finder := &fakeFinder{ids: []string{"doc-tagged", "doc-also-tagged"}}
	n, err := markStaleForEntity(db, finder, collectionTags, "tag1")
	if err != nil {
		t.Fatalf("markStaleForEntity: %v", err)
	}
	if n != 2 {
		t.Fatalf("marked %d documents, want 2", n)
	}

	stale := staleFlags(t, db, "doc-tagged", "doc-also-tagged", "doc-untagged")
	if !stale["doc-tagged"] || !stale["doc-also-tagged"] {
		t.Fatalf("a renamed tag left its documents fresh: %v", stale)
	}
	if stale["doc-untagged"] {
		t.Fatal("a document that does not reference the tag was dated too")
	}

	// Tags are a multi-relation: the filter has to ask whether the list
	// contains the id, not whether it equals it.
	if len(finder.filters) == 0 || finder.filters[0] != "tags.id ?= {:id}" {
		t.Fatalf("tag filter = %v", finder.filters)
	}
}

func TestMarkStaleForEntityCoversTheSingleRelations(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "text")
	if err := Replace(db, sampleState("doc1"), sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	for _, collection := range []string{collectionCorrespondents, collectionDocumentTypes} {
		finder := &fakeFinder{ids: []string{"doc1"}}
		n, err := markStaleForEntity(db, finder, collection, "entity1")
		if err != nil || n != 1 {
			t.Fatalf("%s: marked %d (%v)", collection, n, err)
		}
		if got := finder.filters[0]; got != entityFilters[collection] {
			t.Fatalf("%s filter = %q", collection, got)
		}
	}

	// A collection whose names the header never quotes is not fanned out over.
	finder := &fakeFinder{ids: []string{"doc1"}}
	if n, err := markStaleForEntity(db, finder, "users", "entity1"); err != nil || n != 0 {
		t.Fatalf("unknown collection: marked %d (%v)", n, err)
	}
}

// A tag can be on every document in an archive, so the fan-out has to page
// rather than stop at whatever one query returns.
func TestMarkStaleForEntityPagesThroughEverything(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ids := make([]string, 0, entityFanoutPage+7)
	for i := 0; i < entityFanoutPage+7; i++ {
		id := fmt.Sprintf("doc%04d", i)
		ids = append(ids, id)
		insertDocument(t, db, id, "text")
		if err := Replace(db, sampleState(id), []Chunk{
			{DocumentID: id, Ordinal: 0, Kind: KindHeader, Text: "h", Vector: vector(4, 1)},
		}); err != nil {
			t.Fatalf("Replace %s: %v", id, err)
		}
	}

	finder := &fakeFinder{ids: ids}
	n, err := markStaleForEntity(db, finder, collectionTags, "tag1")
	if err != nil {
		t.Fatalf("markStaleForEntity: %v", err)
	}
	if n != len(ids) {
		t.Fatalf("marked %d of %d documents", n, len(ids))
	}
	last := ids[len(ids)-1]
	if !staleFlags(t, db, last)[last] {
		t.Fatalf("the tail of the fan-out was never reached (%s is still fresh)", last)
	}
}

func TestMarkStaleForEntityReportsALookupFailure(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	finder := &fakeFinder{err: errors.New("database is locked")}

	if _, err := markStaleForEntity(db, finder, collectionTags, "tag1"); err == nil {
		t.Fatal("a failed lookup should be reported, not swallowed")
	}
}

// renamed is what keeps a colour change, or a save that touched nothing, from
// buying an archive a full re-embed.
func TestRenamedOnlyFiresForTheNamesTheHeaderQuotes(t *testing.T) {
	t.Parallel()
	collection := core.NewBaseCollection("tags")
	collection.Fields.Add(
		&core.TextField{Name: "name"},
		&core.TextField{Name: "name_original"},
		&core.TextField{Name: "color"},
	)

	newTag := func() *core.Record {
		record := core.NewRecord(collection)
		record.Id = "tag1"
		record.Set("name", "Insurance")
		record.Set("name_original", "Versicherung")
		record.Set("color", "blue")
		// PostScan is what makes the current values the "original" ones, which
		// is the state a record arrives in from the database.
		if err := record.PostScan(); err != nil {
			t.Fatalf("PostScan: %v", err)
		}
		return record
	}

	if renamed(nil) {
		t.Fatal("no record is not a rename")
	}
	created := core.NewRecord(collection)
	created.Set("name", "Insurance")
	if !renamed(created) {
		t.Fatal("a record with no previous state has to be treated as changed")
	}

	unchanged := newTag()
	if renamed(unchanged) {
		t.Fatal("a save that changed nothing is not a rename")
	}

	recoloured := newTag()
	recoloured.Set("color", "red")
	if renamed(recoloured) {
		t.Fatal("a colour change does not date a single vector")
	}

	for _, field := range entityNameFields {
		record := newTag()
		record.Set(field, "Something else")
		if !renamed(record) {
			t.Fatalf("a change to %s has to date the documents that quote it", field)
		}
	}
}

// staleFields is the list touchesEmbeddedText walks, so anything the header
// renders and it does not name is an edit that leaves a wrong vector in place.
func TestStaleFieldsCoverEveryHeaderInput(t *testing.T) {
	t.Parallel()
	named := map[string]bool{}
	for _, field := range staleFields {
		named[field] = true
	}
	for _, field := range []string{
		"ocr_text", "title", "title_original", "purpose", "purpose_original",
		"summary", "summary_original", "document_type", "correspondent",
		"people_or_organizations", "tags", "document_date",
	} {
		if !named[field] {
			t.Fatalf("%s reaches the embedded text but does not mark it stale", field)
		}
	}
}

// touchesEmbeddedText has to see a relation list change: tags arrive as ids,
// and GetString on one says nothing at all.
func TestTouchesEmbeddedTextSeesATagListChange(t *testing.T) {
	t.Parallel()
	collection := core.NewBaseCollection("documents")
	collection.Fields.Add(
		&core.TextField{Name: "title"},
		&core.TextField{Name: "ocr_text"},
		&core.JSONField{Name: "tags"},
		&core.JSONField{Name: "people_or_organizations"},
	)
	record := core.NewRecord(collection)
	record.Id = "doc1"
	record.Set("title", "Policy")
	record.Set("ocr_text", "body")
	record.Set("tags", []string{"tag1"})
	if err := record.PostScan(); err != nil {
		t.Fatalf("PostScan: %v", err)
	}

	if touchesEmbeddedText(record) {
		t.Fatal("a save that changed nothing must not mark the document stale")
	}
	record.Set("tags", []string{"tag1", "tag2"})
	if !touchesEmbeddedText(record) {
		t.Fatal("a new tag changes the header passage and has to date it")
	}
}
