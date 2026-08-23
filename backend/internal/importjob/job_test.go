package importjob

import (
	"errors"
	"testing"
	"time"
)

func TestAcquirePerOwner(t *testing.T) {
	const ownerA = "owner-a"
	const ownerB = "owner-b"
	r := NewRegistry[int](0)

	if err := r.Acquire(ownerA); err != nil {
		t.Fatal(err)
	}
	if err := r.Acquire(ownerA); !errors.Is(err, ErrBusy) {
		t.Fatalf("same owner should be busy, got %v", err)
	}
	if err := r.Acquire(ownerB); err != nil {
		t.Fatalf("other owner should not be blocked: %v", err)
	}

	r.Release(ownerA)
	if err := r.Acquire(ownerA); err != nil {
		t.Fatalf("owner A should be free after release: %v", err)
	}
}

func TestAcquireRequiresOwner(t *testing.T) {
	r := NewRegistry[int](0)
	if err := r.Acquire(""); err == nil {
		t.Fatal("expected error for empty owner")
	}
	if err := r.Acquire("   "); err == nil {
		t.Fatal("expected error for blank owner")
	}
}

func TestPruneLockedDropsFinishedJobs(t *testing.T) {
	r := NewRegistry[int](0)
	now := time.Now().UTC()
	r.jobs = map[string]*Job[int]{
		"fresh":     {ID: "fresh", Status: StatusCompleted, UpdatedAt: now},
		"stale":     {ID: "stale", Status: StatusCompleted, UpdatedAt: now.Add(-2 * DefaultRetention)},
		"failed":    {ID: "failed", Status: StatusFailed, UpdatedAt: now.Add(-2 * DefaultRetention)},
		"longRun":   {ID: "longRun", Status: StatusRunning, UpdatedAt: now.Add(-9 * DefaultRetention)},
		"nilRecord": nil,
	}

	r.mu.Lock()
	r.pruneLocked(now)
	r.mu.Unlock()

	if _, ok := r.jobs["fresh"]; !ok {
		t.Fatal("expected a recently finished job to be retained")
	}
	if _, ok := r.jobs["longRun"]; !ok {
		t.Fatal("expected a still-running job to be retained regardless of age")
	}
	for _, id := range []string{"stale", "failed", "nilRecord"} {
		if _, ok := r.jobs[id]; ok {
			t.Fatalf("expected %q to be swept", id)
		}
	}
}

func TestStartReportsProgressAndResult(t *testing.T) {
	r := NewRegistry[int](0)
	release := make(chan struct{})
	id, err := r.Start("owner", func(report func(done, total int)) (int, error) {
		report(1, 2)
		<-release
		return 42, nil
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	job := waitForProgress(t, r, id, 1)
	if job.Status != StatusRunning {
		t.Fatalf("status=%q want running", job.Status)
	}
	if job.Progress.Total != 2 {
		t.Fatalf("progress total=%d want 2", job.Progress.Total)
	}
	if err := r.Acquire("owner"); !errors.Is(err, ErrBusy) {
		t.Fatalf("owner should stay busy while the job runs, got %v", err)
	}

	close(release)
	job = waitForStatus(t, r, id, StatusCompleted)
	if job.Result == nil || *job.Result != 42 {
		t.Fatalf("result=%v want 42", job.Result)
	}
	if err := r.Acquire("owner"); err != nil {
		t.Fatalf("owner should be free after the job ends: %v", err)
	}
}

func TestStartKeepsResultOnFailure(t *testing.T) {
	r := NewRegistry[int](0)
	id, err := r.Start("owner", func(func(done, total int)) (int, error) {
		return 7, errors.New("boom")
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	job := waitForStatus(t, r, id, StatusFailed)
	if job.Error != "boom" {
		t.Fatalf("error=%q want boom", job.Error)
	}
	if job.Result == nil || *job.Result != 7 {
		t.Fatalf("partial result lost: %v", job.Result)
	}
}

func TestStartRejectsSecondRunPerOwner(t *testing.T) {
	r := NewRegistry[int](0)
	release := make(chan struct{})
	defer close(release)
	if _, err := r.Start("owner", func(func(done, total int)) (int, error) {
		<-release
		return 0, nil
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := r.Start("owner", func(func(done, total int)) (int, error) { return 0, nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start err=%v want ErrBusy", err)
	}
	if _, err := r.Start("other", func(func(done, total int)) (int, error) { return 0, nil }); err != nil {
		t.Fatalf("other owner should not be blocked: %v", err)
	}
}

func TestGetUnknownJob(t *testing.T) {
	r := NewRegistry[int](0)
	if _, ok := r.Get("nope"); ok {
		t.Fatal("expected unknown job to report false")
	}
}

func waitForStatus(t *testing.T, r *Registry[int], id, want string) Job[int] {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := r.Get(id)
		if ok && job.Status == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach status %q", id, want)
	return Job[int]{}
}

func waitForProgress(t *testing.T, r *Registry[int], id string, done int) Job[int] {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := r.Get(id)
		if ok && job.Progress.Done >= done {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not report progress %d", id, done)
	return Job[int]{}
}

func TestAppendErrorIsCapped(t *testing.T) {
	t.Parallel()

	var errs []string
	for i := 0; i < MaxReportedErrors+10; i++ {
		errs = AppendError(errs, "boom")
	}
	if len(errs) != MaxReportedErrors {
		t.Fatalf("errors=%d want %d", len(errs), MaxReportedErrors)
	}
}
