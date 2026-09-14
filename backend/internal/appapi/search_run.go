package appapi

import (
	"context"
	"sync"
	"time"
)

// detachedRunBudget caps a detached run. The run no longer ends when the client
// goes away, so something else has to end it: without this a provider that
// hangs would keep a goroutine and its API spend alive for as long as the
// process lives. Generous, because the ceiling is a backstop against a stuck
// provider and not a limit on how long research may legitimately take.
const detachedRunBudget = 20 * time.Minute

// runTooLongMessage is what a caller is told when a run hit that ceiling. It
// names no provider: the provider is usually fine, and pointing at it sends
// people to re-check an AI configuration that was never the problem.
const runTooLongMessage = "This run took too long and was stopped."

// searchRuns is what is in flight right now: the cancel func of every run, so
// an explicit cancel can stop one, and a count per conversation, so a page can
// be told its chat is still being worked on.
//
// It exists because a dropped connection and a pressed Cancel button are the
// same closed socket to an HTTP server, and they mean opposite things. Runs
// used to hang off the request context, which read every drop as a cancel and
// threw away work the provider had already been paid for. Now the socket says
// nothing, cancelling is a request of its own, and a client that went away can
// come back and ask whether its answer is still coming.
//
// A count rather than a flag per session: two tabs can be asking the same
// conversation at once, and the first to finish must not report the other's
// run as over.
var searchRuns = struct {
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	sessions map[string]int
}{cancels: map[string]context.CancelFunc{}, sessions: map[string]int{}}

// runKey scopes a run id to its owner, so one account cannot cancel another's
// run by guessing an id.
func runKey(ownerID, runID string) string { return ownerID + "\x00" + runID }

// startDetachedRun derives the context a run executes under: the request's values
// (the provider cache key rides on the context) without its cancellation, plus
// its own budget.
//
// runID may be empty, which costs the run nothing but the ability to be
// cancelled. sessionID is the conversation the run is writing into, and is what
// sessionRunning answers about.
//
// The returned stop must be called when the run finishes; it releases the
// registry entries and the context.
func startDetachedRun(parent context.Context, ownerID, runID, sessionID string) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), detachedRunBudget)
	key := ""
	if runID != "" {
		key = runKey(ownerID, runID)
	}

	searchRuns.mu.Lock()
	if key != "" {
		// A repeated id from the same owner replaces the old entry, and the run
		// it belonged to loses its cancel. Only reachable if a client reuses an
		// id it is supposed to generate fresh per run.
		searchRuns.cancels[key] = cancel
	}
	if sessionID != "" {
		searchRuns.sessions[sessionID]++
	}
	searchRuns.mu.Unlock()

	return ctx, func() {
		searchRuns.mu.Lock()
		if key != "" {
			delete(searchRuns.cancels, key)
		}
		if sessionID != "" {
			if searchRuns.sessions[sessionID] <= 1 {
				delete(searchRuns.sessions, sessionID)
			} else {
				searchRuns.sessions[sessionID]--
			}
		}
		searchRuns.mu.Unlock()
		cancel()
	}
}

// sessionRunning reports whether a run is currently writing into a
// conversation.
//
// This is the only thing that says so: a turn is stored as one user+assistant
// pair when the run finishes, so while it is working the transcript looks
// exactly like a conversation where nothing was ever asked. A page reopened
// mid-run would otherwise show an empty chat and no sign that an answer is on
// its way.
func sessionRunning(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	searchRuns.mu.Lock()
	defer searchRuns.mu.Unlock()
	return searchRuns.sessions[sessionID] > 0
}

// cancelSearchRun stops a run by id. Reports whether there was one: an id that
// is not running is not an error, since a run that just finished on its own
// races with the cancel the client sent.
func cancelSearchRun(ownerID, runID string) bool {
	if runID == "" {
		return false
	}
	searchRuns.mu.Lock()
	cancel, ok := searchRuns.cancels[runKey(ownerID, runID)]
	searchRuns.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
