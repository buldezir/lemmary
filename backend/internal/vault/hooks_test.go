package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lemmary/backend/internal/inflight"
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

// The shutdown flush must wait for work that is still running.
//
// PocketBase's graceful shutdown gives the HTTP server one second and then
// returns whether or not handlers are still going, and cron jobs are fired and
// forgotten and never waited for at all. Without the drain, an upload finishing
// a moment later is answered 200, written into the working directory, and then
// wiped along with it — data acknowledged to a client and lost on a clean
// SIGTERM, which is exactly what docs/encryption.md promises cannot happen.
func TestFinalizeWaitsForInFlightWorkAndCapturesIt(t *testing.T) {
	h := newHarness(t)
	h.write("storage/early.pdf", "%PDF written before shutdown")

	// A handler that is still running when the terminate hook fires, and only
	// finishes its write afterwards.
	done := inflight.Begin()
	written := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.write("storage/late.pdf", "%PDF acknowledged during shutdown")
		close(written)
		done()
	}()

	h.v.Finalize()

	select {
	case <-written:
	default:
		t.Fatal("Finalize flushed without waiting for the in-flight write")
	}

	h.reopen()
	if got := h.read("storage/late.pdf"); got != "%PDF acknowledged during shutdown" {
		t.Fatalf("the write that landed during shutdown did not survive: %q", got)
	}
}

// A drain that times out must still flush. Waiting forever would hang the
// container until Docker escalated to SIGKILL, which loses strictly more.
func TestFinalizeFlushesAnywayWhenTheDrainTimesOut(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a.pdf", "%PDF one")

	stuck := inflight.Begin()
	defer stuck()

	// Shorten the wait rather than sitting through the production timeout.
	orig := drainWait
	drainWait = 50 * time.Millisecond
	defer func() { drainWait = orig }()

	h.v.Finalize()

	if h.v.Generation() == 0 {
		t.Fatal("Finalize gave up on the flush because work was still in flight")
	}
	var warned bool
	for _, line := range h.logs {
		if strings.Contains(line, "still running") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("a clipped shutdown was not reported; logs: %v", h.logs)
	}
}

// A write that lands *during* the flush is not in it, so Finalize goes round
// again rather than sealing an archive it knows is already stale.
func TestFinalizeReflushesWhenWritesLandDuringTheFlush(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a.pdf", "%PDF one")
	if err := h.v.Flush("first"); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	h.write("storage/b.pdf", "%PDF two")
	h.v.MarkDirty()
	h.v.Finalize()

	if h.v.dirty.get() != 0 {
		t.Fatalf("Finalize left %d pending writes", h.v.dirty.get())
	}
	h.reopen()
	if got := h.read("storage/b.pdf"); got != "%PDF two" {
		t.Fatalf("content = %q", got)
	}
}
