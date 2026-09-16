package appapi

import (
	"context"
	"testing"
	"time"
)

// The regression that lost finished answers: a proxy hangup cancelled the agent
// loop and the turn was never stored.
func TestSearchRunSurvivesTheRequestContext(t *testing.T) {
	request, disconnect := context.WithCancel(context.Background())
	ctx, stop := startDetachedRun(request, "owner", "run-1", "session-1")
	defer stop()

	disconnect()

	select {
	case <-ctx.Done():
		t.Fatal("run was cancelled by the client going away")
	case <-time.After(50 * time.Millisecond):
	}
}

// The provider cache key rides on the context, and losing it would silently
// split one conversation across two caches.
func TestSearchRunKeepsContextValues(t *testing.T) {
	type key struct{}
	request := context.WithValue(context.Background(), key{}, "session-7")

	ctx, stop := startDetachedRun(request, "owner", "run-1", "session-1")
	defer stop()

	if got := ctx.Value(key{}); got != "session-7" {
		t.Fatalf("context value = %v, want session-7", got)
	}
}

func TestCancelSearchRunStopsIt(t *testing.T) {
	ctx, stop := startDetachedRun(context.Background(), "owner", "run-1", "session-1")
	defer stop()

	if !cancelSearchRun("owner", "run-1") {
		t.Fatal("cancel did not find the run")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("run kept going after an explicit cancel")
	}
}

// Without the scoping, one account could stop another's research by guessing
// an id.
func TestCancelSearchRunIsScopedToTheOwner(t *testing.T) {
	ctx, stop := startDetachedRun(context.Background(), "owner", "run-1", "session-1")
	defer stop()

	if cancelSearchRun("someone-else", "run-1") {
		t.Fatal("another owner's cancel found the run")
	}
	select {
	case <-ctx.Done():
		t.Fatal("run was cancelled by another owner")
	case <-time.After(50 * time.Millisecond):
	}
}

// The client pressed the button while the last event was already on the wire.
func TestCancelSearchRunAfterItFinished(t *testing.T) {
	_, stop := startDetachedRun(context.Background(), "owner", "run-1", "session-1")
	stop()

	if cancelSearchRun("owner", "run-1") {
		t.Fatal("a finished run is still registered")
	}
}

// Detached all the same; it simply cannot be cancelled, which is the lesser
// loss.
func TestSearchRunWithoutAnIDStillDetaches(t *testing.T) {
	request, disconnect := context.WithCancel(context.Background())
	ctx, stop := startDetachedRun(request, "owner", "", "session-1")
	defer stop()

	disconnect()
	if cancelSearchRun("owner", "") {
		t.Fatal("the empty id matched a run")
	}
	select {
	case <-ctx.Done():
		t.Fatal("run was cancelled by the client going away")
	case <-time.After(50 * time.Millisecond):
	}
}

// The turn is stored whole when the run ends, so a chat reopened mid-run looks
// empty and finished; this is what tells the page otherwise.
func TestSessionRunningWhileARunIsInFlight(t *testing.T) {
	if sessionRunning("session-9") {
		t.Fatal("nothing is running yet")
	}

	_, stop := startDetachedRun(context.Background(), "owner", "run-9", "session-9")
	if !sessionRunning("session-9") {
		t.Fatal("a run is in flight but the session does not say so")
	}

	stop()
	if sessionRunning("session-9") {
		t.Fatal("the run finished and the session still says it is working")
	}
}

// The first of two tabs to finish must not report the other's run as over, or
// the second page stops waiting for an answer that is still coming.
func TestSessionRunningCountsConcurrentRuns(t *testing.T) {
	_, stopFirst := startDetachedRun(context.Background(), "owner", "run-a", "session-8")
	_, stopSecond := startDetachedRun(context.Background(), "owner", "run-b", "session-8")

	stopFirst()
	if !sessionRunning("session-8") {
		t.Fatal("one run of two ended and the session already reads as idle")
	}

	stopSecond()
	if sessionRunning("session-8") {
		t.Fatal("both runs ended and the session still reads as busy")
	}
}

// A page reloaded mid-run never saw the run id, so the conversation is all it
// has to cancel with. Runs registered without an id are reachable this way too.
func TestCancelSessionRunsStopsEveryRunOnTheConversation(t *testing.T) {
	first, stopFirst := startDetachedRun(context.Background(), "owner", "", "session-12")
	defer stopFirst()
	second, stopSecond := startDetachedRun(context.Background(), "owner", "run-12", "session-12")
	defer stopSecond()
	other, stopOther := startDetachedRun(context.Background(), "owner", "run-13", "session-13")
	defer stopOther()

	if !cancelSessionRuns("session-12") {
		t.Fatal("cancel did not find the session's runs")
	}
	for _, ctx := range []context.Context{first, second} {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("a run on the cancelled session kept going")
		}
	}
	select {
	case <-other.Done():
		t.Fatal("a run on another session was cancelled")
	case <-time.After(50 * time.Millisecond):
	}
	if cancelSessionRuns("session-14") || cancelSessionRuns("") {
		t.Fatal("an unknown session matched a run")
	}
}
