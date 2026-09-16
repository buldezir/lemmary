package appapi

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The authorization code a poll returns is single-use, so two overlapping polls
// would both read the same pending login and the second exchange would be
// refused for a sign-in that had just succeeded. The handler wants a live
// PocketBase, so this covers the lock it holds rather than the handler.
func TestLoginPollsAreSerializedPerProvider(t *testing.T) {
	const id = "p-lock"
	var inside, overlaps atomic.Int32

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockLogin(id)
			defer unlock()
			if inside.Add(1) > 1 {
				overlaps.Add(1)
			}
			// Long enough that an unserialized run overlaps reliably.
			time.Sleep(2 * time.Millisecond)
			inside.Add(-1)
		}()
	}
	wg.Wait()

	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d polls ran concurrently for one provider", n)
	}
}

// Per provider, not globally: one operator signing in must not stall another's.
func TestLoginPollsOnDifferentProvidersDoNotBlockEachOther(t *testing.T) {
	firstHeld := lockLogin("p-lock-a")
	defer firstHeld()

	done := make(chan struct{})
	go func() {
		unlock := lockLogin("p-lock-b")
		unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a poll on one provider blocked a poll on another")
	}
}
