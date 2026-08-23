package ngximport

import (
	"errors"
	"testing"
)

// The job machinery itself is covered by internal/importjob; this only asserts
// that ngximport is wired to it and still guards one import per owner.
func TestRunWithClientRejectsConcurrentImports(t *testing.T) {
	const owner = "owner-a"
	if err := registry.Acquire(owner); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { registry.Release(owner) })

	if _, err := RunWithClient(nil, owner, "https://example.com", "key", ModePreserve, nil); !errors.Is(err, ErrImportInProgress) {
		t.Fatalf("err=%v want ErrImportInProgress", err)
	}
	if _, err := RunWithClient(nil, "owner-b", "", "", ModePreserve, nil); errors.Is(err, ErrImportInProgress) {
		t.Fatal("a different owner must not be blocked")
	}
}
