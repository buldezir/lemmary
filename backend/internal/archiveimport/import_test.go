package archiveimport

import "testing"

func TestRemapIDsKeepsOnlyDocumentsRestoredWithIt(t *testing.T) {
	idByExported := map[string]string{"old_a": "new_a", "old_b": "new_b", "old_c": "new_c"}
	exported := []string{"old_b", "not_in_archive", "old_a", "old_c", "old_b"}
	if got := remapIDs(idByExported, "new_a", exported); got != `["new_b","new_c"]` {
		t.Fatalf("remapIDs = %s", got)
	}
	if got := remapIDs(idByExported, "new_a", []string{"not_in_archive"}); got != "" {
		t.Fatalf("remapIDs with no restored target = %q, want empty", got)
	}
}
