// Package inflight counts work that must finish before the process can be
// considered quiescent.
//
// Encryption at rest commits the archive on the way out, while PocketBase's
// graceful shutdown gives handlers a one-second deadline and never waits for
// cron jobs at all. Work finishing after the shutdown flush is acknowledged to
// a client and then wiped with the plaintext working directory: a clean stop
// that silently loses data.
//
// Both the vault and the workers import this, so the flush can wait without
// anything importing the vault, which keeps encryption an isolated feature.
// The package-level tracker is a singleton because there is one process-wide
// notion of "still working"; tests needing isolation construct a Tracker.
package inflight

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Tracker counts active units of work. The zero value is ready to use.
type Tracker struct {
	mu sync.Mutex
	n  int
	// Created lazily by Wait, so a tracker nobody waits on costs one mutex
	// per unit of work.
	idle chan struct{}
}

// Begin records the start of a unit of work and returns the function that ends
// it. That function is safe to call more than once, so it can be deferred
// without the caller reasoning about its own error paths.
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

// Wait blocks until no work is in flight, or until ctx is done. A timeout is
// reported, not swallowed: a caller about to destroy the working directory
// needs to know something is still writing to it.
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

func (t *Tracker) Active() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}

var std Tracker

func Begin() (done func()) { return std.Begin() }

func Wait(ctx context.Context) error { return std.Wait(ctx) }

func Active() int { return std.Active() }

// SealStoreKey is where the vault leaves its Seal in the app store; no Seal
// there means no encryption at rest, and every write is durable at once.
const SealStoreKey = "inflight.seal"

// Seal tracks what encryption at rest has put on the volume. A saved record is
// only there once a flush that began after the save has committed; until then a
// hard kill loses it. Work that destroys the only other copy (a consumed
// original) waits for that.
type Seal struct {
	sealedAt  atomic.Int64
	lastWrite atomic.Int64
}

// NewSeal starts sealed: everything already in the working directory came out
// of the vault.
func NewSeal() *Seal {
	s := &Seal{}
	s.sealedAt.Store(time.Now().UnixNano())
	return s
}

// Wrote records a committed write the vault has yet to seal.
func (s *Seal) Wrote() { s.lastWrite.Store(time.Now().UnixNano()) }

// Sealed records a committed flush that began at startedUnixNano.
func (s *Seal) Sealed(startedUnixNano int64) { s.sealedAt.Store(startedUnixNano) }

// Durable reports whether a write finished at t survives a hard kill: a flush
// began after it, or nothing was written since the last flush began.
func (s *Seal) Durable(t time.Time) bool {
	sealed := s.sealedAt.Load()
	return t.UnixNano() < sealed || s.lastWrite.Load() < sealed
}
