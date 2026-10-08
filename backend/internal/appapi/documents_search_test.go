package appapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/fulltext"
)

// Collecting ids for a scope or an export takes every match at once: a page of
// Search stops at MaxSearchLimit.
func TestDocumentSearchAllReturnsEveryMatchPastAPage(t *testing.T) {
	idx := fulltext.New()
	if err := idx.Open(t.TempDir()); err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	want := fulltext.MaxSearchLimit + 10
	for i := range want {
		if err := idx.Put(fmt.Sprintf("doc%d", i), map[string]any{fulltext.FieldUser: "u1", fulltext.FieldTitle: "Lease"}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	q := fulltext.Query{Text: "lease", UserID: "u1", Limit: want}
	result, err := documentSearch(context.Background(), nil, &config.Runtime{}, idx, q, true)
	if err != nil {
		t.Fatalf("documentSearch: %v", err)
	}
	if len(result.Hits) != want || result.Total != uint64(want) {
		t.Fatalf("all = %d hits of %d, want %d", len(result.Hits), result.Total, want)
	}
	page, err := documentSearch(context.Background(), nil, &config.Runtime{}, idx, q, false)
	if err != nil || len(page.Hits) != fulltext.MaxSearchLimit {
		t.Fatalf("a page = %d hits, err %v, want the %d cap", len(page.Hits), err, fulltext.MaxSearchLimit)
	}
	// A caller with a ceiling learns it is past it without the whole set.
	q.Limit = 3
	capped, err := documentSearch(context.Background(), nil, &config.Runtime{}, idx, q, true)
	if err != nil || len(capped.Hits) != 3 || capped.Total != uint64(want) {
		t.Fatalf("capped = %d hits of %d, err %v, want 3 of %d", len(capped.Hits), capped.Total, err, want)
	}
}

type stubDocuments struct {
	recs map[string]*core.Record
	// shares is keyed "documentID/userID"; absent means the document is not
	// shared with that user.
	shares map[string]bool
}

func (s stubDocuments) FindRecordById(_ any, recordId string, _ ...func(*dbx.SelectQuery) error) (*core.Record, error) {
	rec, ok := s.recs[recordId]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return rec, nil
}

func (s stubDocuments) FindFirstRecordByFilter(_ any, _ string, params ...dbx.Params) (*core.Record, error) {
	if len(params) == 0 {
		return nil, fmt.Errorf("not found")
	}
	document, _ := params[0]["document"].(string)
	user, _ := params[0]["user"].(string)
	if s.shares[document+"/"+user] {
		return core.NewRecord(core.NewBaseCollection("document_shares")), nil
	}
	return nil, fmt.Errorf("not found")
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
