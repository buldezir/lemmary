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
