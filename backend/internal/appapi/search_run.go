package appapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
)

// detachedRunBudget is what ends a run, since the client going away no longer
// does: a hung provider would otherwise keep a goroutine and its API spend
// alive for the life of the process. Generous, because it is a backstop against
// a stuck provider, not a limit on how long research may take.
const detachedRunBudget = 20 * time.Minute

// runTooLongMessage names no provider: the provider is usually fine, and
// pointing at it sends people to re-check a configuration that was never it.
const runTooLongMessage = "This run took too long and was stopped."

// writeRunError answers for a detached run that failed; what names the run in
// the log. Running out of budget is not the provider failing, and saying so
// sends the caller to check an AI configuration that is fine.
func writeRunError(ctx context.Context, e *core.RequestEvent, logger *slog.Logger, what string, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		logger.Warn(what+" ran out of budget", "budget", detachedRunBudget.String())
		return writeError(e, http.StatusGatewayTimeout, runTooLongMessage)
	}
	logger.Error(what+" failed", slog.Any("error", err))
	return writeError(e, http.StatusBadGateway, ai.ProviderErrorMessage(err))
}

// searchRuns holds the cancel func of every run in flight, also grouped per
// conversation. It exists because a dropped connection and a pressed Cancel
// button are the same closed socket to an HTTP server and mean opposite things,
// so cancelling is a request of its own. A set rather than a flag per session:
// two tabs can ask the same conversation at once, and the first to finish must
// not report the other's run as over.
var searchRuns = struct {
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	sessions map[string]map[*context.CancelFunc]struct{}
}{cancels: map[string]context.CancelFunc{}, sessions: map[string]map[*context.CancelFunc]struct{}{}}

// runKey scopes a run id to its owner, so one account cannot cancel another's
// run by guessing an id.
func runKey(ownerID, runID string) string { return ownerID + "\x00" + runID }

// startDetachedRun keeps the request's context values (the provider cache key
// rides on them) without its cancellation, and adds its own budget. An empty
// runID costs only the ability to cancel. The returned stop must be called when
// the run finishes; it releases the registry entries and the context.
func startDetachedRun(parent context.Context, ownerID, runID, sessionID string) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), detachedRunBudget)
	key := ""
	if runID != "" {
		key = runKey(ownerID, runID)
	}

	searchRuns.mu.Lock()
	if key != "" {
		// A repeated id from the same owner replaces the old entry, and that run
		// loses its cancel. Only reachable if a client reuses an id.
		searchRuns.cancels[key] = cancel
	}
	entry := &cancel
	if sessionID != "" {
		runs := searchRuns.sessions[sessionID]
		if runs == nil {
			runs = map[*context.CancelFunc]struct{}{}
			searchRuns.sessions[sessionID] = runs
		}
		runs[entry] = struct{}{}
	}
	searchRuns.mu.Unlock()

	return ctx, func() {
		searchRuns.mu.Lock()
		if key != "" {
			delete(searchRuns.cancels, key)
		}
		if sessionID != "" {
			runs := searchRuns.sessions[sessionID]
			delete(runs, entry)
			if len(runs) == 0 {
				delete(searchRuns.sessions, sessionID)
			}
		}
		searchRuns.mu.Unlock()
		cancel()
	}
}

// sessionRunning is the only thing that says a run is in progress: a turn is
// stored as one pair when the run finishes, so until then the transcript looks
// like a conversation where nothing was ever asked.
func sessionRunning(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	searchRuns.mu.Lock()
	defer searchRuns.mu.Unlock()
	return len(searchRuns.sessions[sessionID]) > 0
}

// cancelSessionRuns is what a page reopened mid-run cancels with: the run id
// lives only in the tab that started the run. The caller must already have
// checked the session belongs to the requester; this registry knows no owners.
//
// ponytail: stops every run on the conversation, a second tab's included; hand
// out per-run ids on the session detail if that ever matters.
func cancelSessionRuns(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	searchRuns.mu.Lock()
	var cancels []context.CancelFunc
	for entry := range searchRuns.sessions[sessionID] {
		cancels = append(cancels, *entry)
	}
	searchRuns.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels) > 0
}

// cancelSearchRun reports whether there was a run: an id that is not running is
// not an error, since a run that just finished races the cancel.
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
