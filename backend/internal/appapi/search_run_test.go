package appapi

import (
	"context"
	"testing"
	"time"
)

// The whole point of the detached run: the client's connection going away must
// not stop it. This is the regression that lost finished answers -- a proxy
// hangup cancelled the agent loop, and the turn was never stored.
func TestSearchRunSurvivesTheRequestContext(t *testing.T) {
	request, disconnect := context.WithCancel(context.Background())
	ctx, stop := startSearchRun(request, "owner", "run-1")
	defer stop()

	disconnect()

	select {
	case <-ctx.Done():
		t.Fatal("run was cancelled by the client going away")
	case <-time.After(50 * time.Millisecond):
	}
}

// Values still cross: the provider cache key rides on the context, and losing
// it would silently split one conversation across two caches.
func TestSearchRunKeepsContextValues(t *testing.T) {
	type key struct{}
	request := context.WithValue(context.Background(), key{}, "session-7")

	ctx, stop := startSearchRun(request, "owner", "run-1")
	defer stop()

	if got := ctx.Value(key{}); got != "session-7" {
		t.Fatalf("context value = %v, want session-7", got)
	}
}

func TestCancelSearchRunStopsIt(t *testing.T) {
	ctx, stop := startSearchRun(context.Background(), "owner", "run-1")
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

// A run id is only meaningful within its owner. Without the scoping, one
// account could stop another's research by guessing an id.
func TestCancelSearchRunIsScopedToTheOwner(t *testing.T) {
	ctx, stop := startSearchRun(context.Background(), "owner", "run-1")
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

// A cancel that arrives after the run finished is not an error: the client
// pressed the button while the last event was already on the wire.
func TestCancelSearchRunAfterItFinished(t *testing.T) {
	_, stop := startSearchRun(context.Background(), "owner", "run-1")
	stop()

	if cancelSearchRun("owner", "run-1") {
		t.Fatal("a finished run is still registered")
	}
}

// A client that sends no id gets a run that is detached all the same. It simply
// cannot be cancelled, which is the lesser loss.
func TestSearchRunWithoutAnIDStillDetaches(t *testing.T) {
	request, disconnect := context.WithCancel(context.Background())
	ctx, stop := startSearchRun(request, "owner", "")
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
