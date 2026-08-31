package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Regression: the shutdown flush must actually run.
//
// The debounce timer is cancelled on the way out, and an earlier version gated
// every flush on that same "stopped" flag — which turned the single most
// important flush in the system into a silent no-op. A clean shutdown then left
// the volume holding nothing but a keyring, losing the whole session.
func TestFinalizeFlushesEvenAfterTheDebounceIsStopped(t *testing.T) {
	h := newHarness(t)
	f := &flusher{v: h.v}

	h.write("storage/precious.pdf", "%PDF must survive shutdown")

	f.stop() // as the terminate hook does first
	h.v.Finalize()

	if h.v.Generation() == 0 {
		t.Fatal("Finalize did not commit a generation after the debounce was stopped")
	}

	h.reopen()
	if got := h.read("storage/precious.pdf"); got != "%PDF must survive shutdown" {
		t.Fatalf("content after shutdown and restart = %q", got)
	}
}

// Finalize must be idempotent: Close calls it again, by which point PocketBase
// has closed the databases and a second snapshot would fail.
func TestFinalizeIsIdempotentAndCloseSkipsTheSecondFlush(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a.pdf", "%PDF one")

	h.v.Finalize()
	gen := h.v.Generation()
	if gen == 0 {
		t.Fatal("Finalize did not flush")
	}

	// A snapshotter that fails stands in for the closed databases.
	h.v.SetSnapshotter(failingSnapshotter{})
	h.v.Finalize()
	if h.v.Generation() != gen {
		t.Fatal("a second Finalize flushed again")
	}
	if err := h.v.Close(); err != nil {
		t.Fatalf("Close after Finalize: %v", err)
	}
	if _, err := os.Stat(h.workDir); !os.IsNotExist(err) {
		t.Fatal("Close did not wipe the working directory")
	}
}

type failingSnapshotter struct{}

func (failingSnapshotter) SnapshotDatabases(string) error {
	return os.ErrClosed
}

// If the shutdown flush fails, the plaintext must be kept — it holds data the
// vault does not.
func TestFinalizeFailureKeepsTheWorkingDirectory(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a.pdf", "%PDF one")
	h.v.SetSnapshotter(failingSnapshotter{})

	h.v.Finalize()
	if err := h.v.Close(); err == nil {
		t.Fatal("Close reported success after a failed shutdown flush")
	}
	if _, err := os.Stat(filepath.Join(h.workDir, "storage/a.pdf")); err != nil {
		t.Fatalf("unflushed data was wiped: %v", err)
	}
}

// Enrollment hooks run on PocketBase's concurrent request goroutines, so
// keyring mutation has to be serialised. Before UpdateKeyring existed each
// hook did an unsynchronised read-modify-write of the wrap list and the last
// save won — a user whose wrap lost that race simply could not unlock after
// the next restart. Run with -race this also catches the data race itself.
func TestConcurrentEnrollmentKeepsEveryWrap(t *testing.T) {
	h := newHarness(t)

	const users = 32
	var wg sync.WaitGroup
	for i := 0; i < users; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := h.v.UpdateKeyring(func(kr *Keyring) error {
				return kr.AddPassword(h.mk, fmt.Sprintf("user%02d", i), fmt.Sprintf("password-%02d", i))
			})
			if err != nil {
				t.Errorf("enroll user%02d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Every wrap must be present in memory and in the keyring that was saved.
	reloaded, err := LoadKeyring(h.dir)
	if err != nil {
		t.Fatalf("reload keyring: %v", err)
	}
	for i := 0; i < users; i++ {
		id := fmt.Sprintf("user%02d", i)
		if !h.v.Keyring().HasWrapForUser(id) {
			t.Errorf("in-memory keyring lost the wrap for %s", id)
		}
		if !reloaded.HasWrapForUser(id) {
			t.Errorf("persisted keyring lost the wrap for %s", id)
		}
	}

	// And each survivor must actually open the vault.
	if _, _, err := reloaded.Unlock(Credential{Password: "password-07"}); err != nil {
		t.Fatalf("a concurrently enrolled credential cannot unlock: %v", err)
	}
}
