package ngximport

import (
	"errors"
	"testing"
	"time"
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

func TestPruneJobsLockedDropsFinishedJobs(t *testing.T) {
	jobsMu.Lock()
	defer jobsMu.Unlock()

	now := time.Now().UTC()
	original := jobs
	jobs = map[string]*Job{
		"fresh":     {ID: "fresh", Status: JobStatusCompleted, UpdatedAt: now},
		"stale":     {ID: "stale", Status: JobStatusCompleted, UpdatedAt: now.Add(-2 * jobRetention)},
		"failed":    {ID: "failed", Status: JobStatusFailed, UpdatedAt: now.Add(-2 * jobRetention)},
		"longRun":   {ID: "longRun", Status: JobStatusRunning, UpdatedAt: now.Add(-9 * jobRetention)},
		"nilRecord": nil,
	}
	defer func() { jobs = original }()

	pruneJobsLocked(now)

	if _, ok := jobs["fresh"]; !ok {
		t.Fatal("expected a recently finished job to be retained")
	}
	if _, ok := jobs["longRun"]; !ok {
		t.Fatal("expected a still-running job to be retained regardless of age")
	}
	for _, id := range []string{"stale", "failed", "nilRecord"} {
		if _, ok := jobs[id]; ok {
			t.Fatalf("expected %q to be swept", id)
		}
	}
}
