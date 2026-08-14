package ngximport

import (
	"errors"
	"testing"
)

func TestAcquireImportPerOwner(t *testing.T) {
	const ownerA = "owner-a"
	const ownerB = "owner-b"
	t.Cleanup(func() {
		releaseImport(ownerA)
		releaseImport(ownerB)
	})

	if err := acquireImport(ownerA); err != nil {
		t.Fatal(err)
	}
	if err := acquireImport(ownerA); !errors.Is(err, ErrImportInProgress) {
		t.Fatalf("same owner should be busy, got %v", err)
	}
	if err := acquireImport(ownerB); err != nil {
		t.Fatalf("other owner should not be blocked: %v", err)
	}

	releaseImport(ownerA)
	if err := acquireImport(ownerA); err != nil {
		t.Fatalf("owner A should be free after release: %v", err)
	}
}

func TestAcquireImportRequiresOwner(t *testing.T) {
	if err := acquireImport(""); err == nil {
		t.Fatal("expected error for empty owner")
	}
	if err := acquireImport("   "); err == nil {
		t.Fatal("expected error for blank owner")
	}
}
