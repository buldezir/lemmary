package appapi

import (
	"fmt"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/fulltext"
)

type stubDocuments struct {
	recs map[string]*core.Record
}

func (s stubDocuments) FindRecordById(_ any, recordId string, _ ...func(*dbx.SelectQuery) error) (*core.Record, error) {
	rec, ok := s.recs[recordId]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return rec, nil
}

func (s stubDocuments) ExpandRecord(*core.Record, []string, core.ExpandFetchFunc) map[string]error {
	return nil
}

func testDocumentRecord(id, user, title string) *core.Record {
	col := core.NewBaseCollection("documents")
	col.Fields.Add(
		&core.TextField{Name: "user"},
		&core.TextField{Name: "title"},
	)
	rec := core.NewRecord(col)
	rec.Id = id
	rec.Set("user", user)
	rec.Set("title", title)
	return rec
}

func TestHydrateDocumentExportsRejectsStaleOwner(t *testing.T) {
	doc := testDocumentRecord("doc1", "new-owner", "Secret lease")
	app := stubDocuments{recs: map[string]*core.Record{"doc1": doc}}
	hits := []fulltext.Hit{{ID: "doc1"}}

	got := hydrateDocumentExports(app, hits, "stale-owner")
	if len(got) != 0 {
		t.Fatalf("stale owner should not receive the record, got %#v", got)
	}

	asOwner := hydrateDocumentExports(app, hits, "new-owner")
	if len(asOwner) != 1 {
		t.Fatalf("current owner should receive the record, got %#v", asOwner)
	}

	asSuper := hydrateDocumentExports(app, hits, "")
	if len(asSuper) != 1 {
		t.Fatalf("superuser should receive the record, got %#v", asSuper)
	}
}
