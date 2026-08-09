package appapi

import "testing"

func TestUpsertPairedUserRejectsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := UpsertPairedUser(nil, "", "password123"); err == nil {
		t.Fatal("expected email required error")
	}
	if _, err := UpsertPairedUser(nil, "a@b.co", ""); err == nil {
		t.Fatal("expected password required error")
	}
}

func TestRevokePairedAdminEmptyEmail(t *testing.T) {
	t.Parallel()
	if err := RevokePairedAdmin(nil, "  "); err != nil {
		t.Fatalf("empty email: %v", err)
	}
}
