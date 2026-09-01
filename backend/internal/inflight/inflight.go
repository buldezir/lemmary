// Package inflight counts work that must finish before the process can be
// considered quiescent.
//
// It exists for one reason. Encryption at rest commits the archive on the way
// out, and PocketBase's own graceful shutdown gives the HTTP server a one-second
// deadline and then returns whether or not handlers are still running; cron jobs
// are fired and forgotten and are not waited for at all. Work that completes
// after the shutdown flush has already been acknowledged to a client, and is
// then wiped along with the plaintext working directory — a clean stop that
// silently loses data.
//
// Counting the work in a package both the vault and the worker can import lets
// the shutdown flush wait for it without the vault knowing what the work is, and
// without anything having to import the vault, which is what keeps encryption an
// isolated feature.
//
// The tracker is a package-level singleton on purpose: there is exactly one
// process-wide notion of "still working", and threading an instance from main
// through every producer would add plumbing to each call site to express
// something that is not actually configurable. Tests that need isolation
// construct their own Tracker.
package inflight

import (
	"context"
	"sync"
)

// Tracker counts active units of work and lets a caller wait for them to finish.
//
// The zero value is ready to use.
type Tracker struct {
	mu sync.Mutex
	n  int
	// idle is created lazily by Wait and closed when the count reaches zero, so
	// a tracker nobody waits on costs one mutex per unit of work.
	idle chan struct{}
}

// Begin records the start of a unit of work and returns the function that ends
// it. The returned function is safe to call more than once, so it can be
// deferred without the caller reasoning about its own error paths.
func (t *Tracker) Begin() (done func()) {
	t.mu.Lock()
	t.n++
	t.mu.Unlock()

	var once sync.Once
	return func() { once.Do(t.end) }
}

func (t *Tracker) end() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.n--
	if t.n == 0 && t.idle != nil {
		close(t.idle)
		t.idle = nil
	}
}

// Wait blocks until no work is in flight, or until ctx is done.
//
// It reports ctx's error on a timeout rather than pretending the wait
// succeeded: a caller about to destroy the working directory needs to know that
// something is still writing to it.
func (t *Tracker) Wait(ctx context.Context) error {
	t.mu.Lock()
	if t.n == 0 {
		t.mu.Unlock()
		return nil
	}
	if t.idle == nil {
		t.idle = make(chan struct{})
	}
	idle := t.idle
	t.mu.Unlock()

	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Active reports how many units of work are in flight.
func (t *Tracker) Active() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}

// std is the process-wide tracker the package functions operate on.
var std Tracker

// Begin records a unit of work on the process-wide tracker.
func Begin() (done func()) { return std.Begin() }

// Wait blocks until the process-wide tracker is idle, or ctx is done.
func Wait(ctx context.Context) error { return std.Wait(ctx) }

// Active reports how much work the process-wide tracker is counting.
func Active() int { return std.Active() }
