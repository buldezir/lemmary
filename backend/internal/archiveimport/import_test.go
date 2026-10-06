package archiveimport

import "testing"

func TestRemapRelatedKeepsOnlyDocumentsRestoredWithIt(t *testing.T) {
	idByExported := map[string]string{"old_a": "new_a", "old_b": "new_b", "old_c": "new_c"}
	doc := restoredDocument{
		NewID:           "new_a",
		ExportedID:      "old_a",
		RelatedExported: []string{"old_b", "not_in_archive", "old_a", "old_c", "old_b"},
	}
	if got := remapRelated(idByExported, doc); got != `["new_b","new_c"]` {
		t.Fatalf("remapRelated = %s", got)
	}
	doc.RelatedExported = []string{"not_in_archive"}
	if got := remapRelated(idByExported, doc); got != "" {
		t.Fatalf("remapRelated with no restored target = %q, want empty", got)
	}
}
