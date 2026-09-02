package worker

import (
	"errors"
	"testing"
	"time"

	"lemmary/backend/internal/config"
)

// fixedClock hands out a controllable now, so the deadline rules can be
// asserted without a test that actually waits half an hour.
type fixedClock struct {
	now  time.Time
	step time.Duration
}

func (c *fixedClock) Now() time.Time {
	out := c.now
	c.now = c.now.Add(c.step)
	return out
}

func TestSweepLoopKeepsGoingUntilTheBacklogIsEmpty(t *testing.T) {
	t.Parallel()
	batches := []batchResult{
		{Candidates: 20, Embedded: 20},
		{Candidates: 20, Embedded: 18, Failed: 2},
		{Candidates: 7, Embedded: 7},
		{},
	}
	calls := 0

	summary, err := sweepLoop(time.Now, time.Now().Add(time.Hour), func() (batchResult, error) {
		res := batches[calls]
		calls++
		return res, nil
	})
	if err != nil {
		t.Fatalf("sweepLoop: %v", err)
	}
	if calls != len(batches) {
		t.Fatalf("ran %d batches, want %d: the sweep must ask again until a batch finds nothing", calls, len(batches))
	}
	if summary.Embedded != 45 || summary.Failed != 2 || summary.Batches != 4 {
		t.Fatalf("summary = %+v", summary)
	}
}

// The candidate query and the freshness check can disagree -- a document listed
// as needing embedding that EmbedDocument then skips. Without this rule the
// sweep would ask for the same batch until its deadline.
func TestSweepLoopStopsWhenABatchMovesNothing(t *testing.T) {
	t.Parallel()
	calls := 0

	summary, err := sweepLoop(time.Now, time.Now().Add(time.Hour), func() (batchResult, error) {
		calls++
		return batchResult{Candidates: 5, Skipped: 5}, nil
	})
	if err != nil {
		t.Fatalf("sweepLoop: %v", err)
	}
	if calls != 1 {
		t.Fatalf("ran %d batches, want 1: a batch that embedded nothing would repeat forever", calls)
	}
	if summary.Skipped != 5 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestSweepLoopStopsAtItsDeadline(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: start, step: time.Minute}
	calls := 0

	summary, err := sweepLoop(clock.Now, start.Add(3*time.Minute), func() (batchResult, error) {
		calls++
		// Always more work to do: only the clock can end this sweep.
		return batchResult{Candidates: 20, Embedded: 20}, nil
	})
	if err != nil {
		t.Fatalf("sweepLoop: %v", err)
	}
	if calls != 3 {
		t.Fatalf("ran %d batches, want 3 before the deadline", calls)
	}
	if summary.Batches != 3 {
		t.Fatalf("summary = %+v", summary)
	}
}

// A sweep that starts after its own deadline embeds nothing rather than one
// batch: the budget is a ceiling, not a minimum.
func TestSweepLoopRunsNothingPastTheDeadline(t *testing.T) {
	t.Parallel()
	now := time.Now()
	calls := 0

	if _, err := sweepLoop(func() time.Time { return now }, now, func() (batchResult, error) {
		calls++
		return batchResult{}, nil
	}); err != nil {
		t.Fatalf("sweepLoop: %v", err)
	}
	if calls != 0 {
		t.Fatalf("ran %d batches, want 0", calls)
	}
}

func TestSweepLoopReportsABatchFailure(t *testing.T) {
	t.Parallel()
	boom := errors.New("candidates query failed")
	calls := 0

	summary, err := sweepLoop(time.Now, time.Now().Add(time.Hour), func() (batchResult, error) {
		calls++
		if calls == 2 {
			return batchResult{}, boom
		}
		return batchResult{Candidates: 20, Embedded: 20}, nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if calls != 2 {
		t.Fatalf("ran %d batches, want 2: a failing batch ends the sweep", calls)
	}
	// The work done before the failure is still reported, so the log line and
	// the response are not a lie.
	if summary.Embedded != 20 {
		t.Fatalf("summary = %+v", summary)
	}
}

// Two sweeps would pick the same candidates and pay for them twice.
func TestStartSweepRefusesToOverlapItself(t *testing.T) {
	t.Parallel()
	// A nil app is the assertion: a second sweep that got past the guard would
	// reach the database and panic instead of returning false.
	b := &Backfiller{rt: &config.Runtime{}}
	b.sweeping.Store(true)

	if b.StartSweep() {
		t.Fatal("StartSweep started a second sweep while one was running")
	}
	if !b.SweepRunning() {
		t.Fatal("SweepRunning() = false while a sweep is in flight")
	}
}

// The cron shares the sweep's mutex, so a tick during a sweep has to give up
// rather than embed the same batch alongside it.
func TestCronTickSkipsWhileASweepHoldsTheLock(t *testing.T) {
	t.Parallel()
	// Nil app again: a tick that took the lock would dereference it.
	b := &Backfiller{}
	b.running.Lock()
	defer b.running.Unlock()

	b.tick()
}

// With no model bound there is nothing to embed, and the sweep must work that
// out before it touches the app -- this is the state a fresh install is in.
func TestSweepWithoutAnEmbedderTouchesNothing(t *testing.T) {
	t.Parallel()
	b := &Backfiller{rt: &config.Runtime{}}

	if summary := b.sweep(); summary.Batches != 0 {
		t.Fatalf("summary = %+v, want an empty sweep", summary)
	}
}

func TestStartSweepClearsItsFlagWhenTheSweepEnds(t *testing.T) {
	t.Parallel()
	b := &Backfiller{rt: &config.Runtime{}}

	if !b.StartSweep() {
		t.Fatal("StartSweep() = false with no sweep running")
	}
	deadline := time.Now().Add(2 * time.Second)
	for b.SweepRunning() {
		if time.Now().After(deadline) {
			t.Fatal("the sweep flag was never cleared; the button would stay disabled forever")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !b.StartSweep() {
		t.Fatal("a finished sweep must not block the next one")
	}
}

// EMBEDDING_BACKFILL_BATCH=0 is how an operator turns the schedule off. The
// Management sweep is an explicit click, so it still has to have a batch size.
func TestSweepBatchSurvivesADisabledCron(t *testing.T) {
	t.Parallel()
	cases := map[int]int{0: defaultBackfillBatch, -5: defaultBackfillBatch, 7: 7}
	for batch, want := range cases {
		b := &Backfiller{batch: batch}
		if got := b.sweepBatch(); got != want {
			t.Fatalf("sweepBatch() with batch=%d = %d, want %d", batch, got, want)
		}
	}
}
