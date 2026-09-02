package appapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/pocketbase/dbx"

	"lemmary/backend/internal/ai"
)

// countDB is a bare documents table with the columns counting reads, in the
// shapes PocketBase writes them: dates as text in two formats, tags as a JSON
// array in a text column, and one legacy empty-string tags value.
func countDB(t *testing.T) dbx.Builder {
	t.Helper()
	db, err := dbx.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.NewQuery(`CREATE TABLE documents (
		id TEXT PRIMARY KEY, user TEXT, title TEXT, document_date TEXT,
		document_type TEXT, correspondent TEXT, tags TEXT, ocr_text TEXT)`).Execute()
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	rows := []struct {
		id, user, date, typ, corr, tags string
	}{
		{"d1", "me", "2025-01-15", "invoice", "acme", `["tax","paid"]`},
		{"d2", "me", "2025-03-02 00:00:00.000Z", "invoice", "acme", `["paid"]`},
		{"d3", "me", "2025-11-30", "invoice", "other", `[]`},
		{"d4", "me", "2024-12-31", "letter", "acme", `["tax"]`},
		{"d5", "me", "", "letter", "", ``},
		{"d6", "you", "2025-05-05", "invoice", "acme", `["paid"]`},
	}
	for _, r := range rows {
		_, err := db.NewQuery(`INSERT INTO documents (id, user, title, document_date, document_type, correspondent, tags, ocr_text)
			VALUES ({:id}, {:user}, {:title}, {:date}, {:typ}, {:corr}, {:tags}, '')`).Bind(dbx.Params{
			"id": r.id, "user": r.user, "title": "Doc " + r.id, "date": r.date, "typ": r.typ, "corr": r.corr, "tags": r.tags,
		}).Execute()
		if err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	return db
}

func groupCounts(rows []countRow) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[r.Key] = r.Count
	}
	return out
}

func TestCountDocumentsScopesToTheOwner(t *testing.T) {
	db := countDB(t)
	_, total, err := countDocuments(context.Background(), db, countSpec{userID: "me"})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want the owner's 5 documents", total)
	}
}

func TestCountDocumentsDateRangeExcludesUndated(t *testing.T) {
	db := countDB(t)
	// An upper bound alone used to admit every undated document, because ''
	// sorts below any date.
	_, total, err := countDocuments(context.Background(), db, countSpec{userID: "me", dateTo: "2025-12-31"})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 4 {
		t.Fatalf("total = %d, want 4 dated documents up to 2025", total)
	}
	_, in2025, err := countDocuments(context.Background(), db, countSpec{userID: "me", dateFrom: "2025-01-01", dateTo: "2025-12-31"})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if in2025 != 3 {
		t.Fatalf("2025 = %d, want 3 (the timestamped date included)", in2025)
	}
}

func TestCountDocumentsGroupsByYearMonthTypeAndCorrespondent(t *testing.T) {
	db := countDB(t)
	ctx := context.Background()

	byYear, _, err := countDocuments(ctx, db, countSpec{userID: "me", groupBy: "year"})
	if err != nil {
		t.Fatalf("year: %v", err)
	}
	if got := groupCounts(byYear); got["2025"] != 3 || got["2024"] != 1 || got[""] != 1 {
		t.Fatalf("by year = %v", got)
	}

	byMonth, _, err := countDocuments(ctx, db, countSpec{userID: "me", groupBy: "month", dateFrom: "2025-01-01"})
	if err != nil {
		t.Fatalf("month: %v", err)
	}
	if got := groupCounts(byMonth); got["2025-01"] != 1 || got["2025-03"] != 1 || got["2025-11"] != 1 || len(got) != 3 {
		t.Fatalf("by month = %v", got)
	}

	byType, total, err := countDocuments(ctx, db, countSpec{userID: "me", groupBy: "document_type"})
	if err != nil {
		t.Fatalf("type: %v", err)
	}
	if got := groupCounts(byType); got["invoice"] != 3 || got["letter"] != 2 || total != 5 {
		t.Fatalf("by type = %v total=%d", got, total)
	}

	byCorr, _, err := countDocuments(ctx, db, countSpec{userID: "me", groupBy: "correspondent", documentTypeIDs: []string{"invoice"}})
	if err != nil {
		t.Fatalf("correspondent: %v", err)
	}
	if got := groupCounts(byCorr); got["acme"] != 2 || got["other"] != 1 {
		t.Fatalf("by correspondent = %v", got)
	}
}

func TestCountDocumentsTagsFilterAndGroupSurviveInvalidJSON(t *testing.T) {
	db := countDB(t)
	ctx := context.Background()

	_, tagged, err := countDocuments(ctx, db, countSpec{userID: "me", tagIDs: []string{"tax"}})
	if err != nil {
		t.Fatalf("tag filter: %v", err)
	}
	if tagged != 2 {
		t.Fatalf("tagged tax = %d, want 2", tagged)
	}

	byTag, total, err := countDocuments(ctx, db, countSpec{userID: "me", groupBy: "tag"})
	if err != nil {
		t.Fatalf("group by tag: %v", err)
	}
	got := groupCounts(byTag)
	// d5's tags column is the legacy empty string: it must count as untagged
	// rather than abort the query.
	if got["tax"] != 2 || got["paid"] != 2 || got[""] != 2 {
		t.Fatalf("by tag = %v", got)
	}
	if total != 5 {
		t.Fatalf("total under group-by-tag = %d, want documents not (document, tag) pairs", total)
	}
}

func TestCountDocumentsOverAnIDSetChunks(t *testing.T) {
	db := countDB(t)
	ids := []string{"d1", "d2", "d6"}
	for i := 0; i < 600; i++ {
		ids = append(ids, fmt.Sprintf("missing%d", i))
	}
	rows, total, err := countDocuments(context.Background(), db, countSpec{userID: "me", groupBy: "correspondent", ids: ids})
	if err != nil {
		t.Fatalf("count over ids: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want the two owned ids (d6 belongs to someone else)", total)
	}
	if got := groupCounts(rows); got["acme"] != 2 || len(got) != 1 {
		t.Fatalf("rows = %v", got)
	}
}

// countApp gives the retriever's stub app a database, which is the one thing
// counting needs beyond what search and read use.
type countApp struct {
	stubRetrieverApp
	db dbx.Builder
}

func (a countApp) DB() dbx.Builder { return a.db }

func TestCountToolResolvesFiltersAndCountsFromTheIndexWithText(t *testing.T) {
	r := hybridRetriever(t, nil)
	db := countDB(t)
	// The hybrid index holds two documents for u1; the database rows above
	// are for a different owner, so a filters-only count here is zero and a
	// text count goes to the index.
	r.app = countApp{stubRetrieverApp: r.app.(stubRetrieverApp), db: db}

	result, err := r.count(context.Background(), ai.CountArgs{Query: "insurance premium"})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("strict text count = %d, want the one document with both words", result.Count)
	}

	result, err = r.count(context.Background(), ai.CountArgs{Tags: []string{"nonexistent"}})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if result.Count != 0 || len(result.Unresolved) != 1 {
		t.Fatalf("an unknown tag should count zero and be named: %+v", result)
	}
}

func TestCountToolWithoutADatabaseIsRefused(t *testing.T) {
	r := hybridRetriever(t, nil)
	if _, err := r.count(context.Background(), ai.CountArgs{}); err == nil {
		t.Fatal("counting needs a database")
	}
}
