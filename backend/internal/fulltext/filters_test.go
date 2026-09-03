package fulltext

import "testing"

// TestEligibleIDsResolvesFiltersWithoutText is the bridge to the chunk index:
// the agent's filters are document properties the chunk index does not carry,
// so they have to be answerable as a list of ids.
func TestEligibleIDsResolvesFiltersWithoutText(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "tagged", map[string]any{
		FieldUser: "u1", FieldTags: []string{"tag1"}, FieldTitle: "Lease", FieldAll: "Lease",
	})
	mustPut(t, idx, "untagged", map[string]any{
		FieldUser: "u1", FieldTitle: "Invoice", FieldAll: "Invoice",
	})
	mustPut(t, idx, "theirs", map[string]any{
		FieldUser: "u2", FieldTags: []string{"tag1"}, FieldTitle: "Lease", FieldAll: "Lease",
	})

	q := Query{UserID: "u1", TagIDs: []string{"tag1"}}
	if !HasDocumentFilters(q) {
		t.Fatal("a tag filter is a document filter")
	}
	if HasDocumentFilters(Query{UserID: "u1"}) {
		t.Fatal("ownership alone is not a document filter: the chunk index applies it itself")
	}

	ids, complete, err := idx.EligibleIDs(q, 10)
	if err != nil {
		t.Fatalf("eligible ids: %v", err)
	}
	if !complete || len(ids) != 1 || ids[0] != "tagged" {
		t.Fatalf("ids = %v, complete = %v", ids, complete)
	}

	// A limit smaller than the result set has to say so, or the caller would
	// pre-filter a dense search down to an arbitrary page of the archive.
	broad := Query{TagIDs: []string{"tag1"}}
	if ids, complete, err := idx.EligibleIDs(broad, 1); err != nil || complete || len(ids) != 1 {
		t.Fatalf("truncation was not reported: ids = %v, complete = %v, err = %v", ids, complete, err)
	}

	// A query that filters nothing resolves to nothing, and the caller sends no
	// pre-filter at all rather than the whole archive.
	if ids, complete, err := idx.EligibleIDs(Query{}, 10); err != nil || !complete || len(ids) != 0 {
		t.Fatalf("an unfiltered query should resolve to nothing: %v, %v, %v", ids, complete, err)
	}
}

func TestKeepEligibleFiltersAShortList(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "tagged", map[string]any{
		FieldUser: "u1", FieldTags: []string{"tag1"}, FieldAll: "Lease",
	})
	mustPut(t, idx, "untagged", map[string]any{FieldUser: "u1", FieldAll: "Invoice"})

	kept, err := idx.KeepEligible(Query{UserID: "u1", TagIDs: []string{"tag1"}}, []string{"untagged", "tagged", "gone"})
	if err != nil {
		t.Fatalf("keep eligible: %v", err)
	}
	if len(kept) != 1 || kept[0] != "tagged" {
		t.Fatalf("kept = %v", kept)
	}

	// No filters means nothing to check, and the list comes back untouched.
	same, err := idx.KeepEligible(Query{UserID: "u1"}, []string{"untagged", "tagged"})
	if err != nil || len(same) != 2 {
		t.Fatalf("an unfiltered query should keep every id: %v, %v", same, err)
	}
}

// TestFieldsRestrictsWhichFieldsMatch is what lets the paperless-ngx
// compatibility layer answer title__icontains and content__icontains
// differently. Without it every text query searches every field, so a
// title-only filter would also match the OCR body and report the result as
// though the filter had been applied.
func TestFieldsRestrictsWhichFieldsMatch(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "titled", map[string]any{
		FieldUser: "u1", FieldTitle: "Lease", FieldOCRText: "nothing relevant", FieldAll: "Lease",
	})
	mustPut(t, idx, "bodied", map[string]any{
		FieldUser: "u1", FieldTitle: "Invoice", FieldOCRText: "the lease runs to 2030", FieldAll: "Invoice",
	})

	// Unrestricted, both match -- that is the archive-wide search box.
	if ids := searchIDs(t, idx, Query{UserID: "u1", Text: "lease"}); len(ids) != 2 {
		t.Fatalf("unrestricted ids = %v, want both documents", ids)
	}

	titleOnly := searchIDs(t, idx, Query{
		UserID: "u1", Text: "lease", Fields: []string{FieldTitle, FieldTitleOriginal},
	})
	if len(titleOnly) != 1 || titleOnly[0] != "titled" {
		t.Fatalf("title-only ids = %v, want [titled]", titleOnly)
	}

	contentOnly := searchIDs(t, idx, Query{
		UserID: "u1", Text: "lease", Fields: []string{FieldOCRText},
	})
	if len(contentOnly) != 1 || contentOnly[0] != "bodied" {
		t.Fatalf("content-only ids = %v, want [bodied]", contentOnly)
	}
}

// TestSearchFieldsKeepsTheTableOrderAndBoosts: the restriction selects from the
// boost table rather than rebuilding it, so a restricted query still ranks the
// fields it kept exactly as an unrestricted one would.
func TestSearchFieldsKeepsTheTableOrderAndBoosts(t *testing.T) {
	t.Parallel()
	if got := searchFields(nil); len(got) != len(boostedTextFields) {
		t.Fatalf("no restriction selected %d fields, want the whole table", len(got))
	}
	got := searchFields([]string{FieldOCRText, FieldTitle})
	if len(got) != 2 || got[0].field != FieldTitle || got[1].field != FieldOCRText {
		t.Fatalf("searchFields = %+v, want title then ocr_text in table order", got)
	}
	if got[0].boost <= got[1].boost {
		t.Fatalf("boosts were not preserved: %+v", got)
	}
	// An unknown name selects nothing rather than quietly widening back to
	// every field, which would turn a title filter into an archive search.
	if got := searchFields([]string{"no_such_field"}); len(got) != 0 {
		t.Fatalf("searchFields(unknown) = %+v, want none", got)
	}
}
