package appapi

import (
	"context"
	"sync"
	"time"
)

// searchRunBudget caps a detached run. The run no longer ends when the client
// goes away, so something else has to end it: without this a provider that
// hangs would keep a goroutine and its API spend alive for as long as the
// process lives. Generous, because the ceiling is a backstop against a stuck
// provider and not a limit on how long research may legitimately take.
const searchRunBudget = 20 * time.Minute

// runTooLongMessage is what a caller is told when a run hit that ceiling. It
// names no provider: the provider is usually fine, and pointing at it sends
// people to re-check an AI configuration that was never the problem.
const runTooLongMessage = "This run took too long and was stopped."

// searchRuns holds the cancel func of every run currently in flight, so an
// explicit cancel request can stop one.
//
// This exists because a dropped connection and a pressed Cancel button are the
// same closed socket to an HTTP server, and they mean opposite things. Runs
// used to hang off the request context, which read every drop as a cancel and
// threw away work the provider had already been paid for. Now the socket says
// nothing and cancelling is a request of its own.
var searchRuns = struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}{m: map[string]context.CancelFunc{}}

// runKey scopes a run id to its owner, so one account cannot cancel another's
// run by guessing an id.
func runKey(ownerID, runID string) string { return ownerID + "\x00" + runID }

// startSearchRun derives the context a run executes under: the request's values
// (the provider cache key rides on the context) without its cancellation, plus
// its own budget.
//
// The returned stop must be called when the run finishes; it releases the
// registry entry and the context.
func startSearchRun(parent context.Context, ownerID, runID string) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), searchRunBudget)
	if runID == "" {
		// Nothing to cancel it by. Still detached -- an un-cancellable run is
		// better than one a flaky network can destroy.
		return ctx, cancel
	}

	key := runKey(ownerID, runID)
	searchRuns.mu.Lock()
	// A repeated id from the same owner replaces the old entry, and the run it
	// belonged to loses its cancel. Only reachable if a client reuses an id it
	// is supposed to generate fresh per run.
	searchRuns.m[key] = cancel
	searchRuns.mu.Unlock()

	return ctx, func() {
		searchRuns.mu.Lock()
		delete(searchRuns.m, key)
		searchRuns.mu.Unlock()
		cancel()
	}
}

// cancelSearchRun stops a run by id. Reports whether there was one: an id that
// is not running is not an error, since a run that just finished on its own
// races with the cancel the client sent.
func cancelSearchRun(ownerID, runID string) bool {
	if runID == "" {
		return false
	}
	searchRuns.mu.Lock()
	cancel, ok := searchRuns.m[runKey(ownerID, runID)]
	searchRuns.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
