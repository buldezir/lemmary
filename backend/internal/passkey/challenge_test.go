package passkey

import (
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

func testSession(challenge string) *webauthn.SessionData {
	return &webauthn.SessionData{
		Challenge: challenge,
		UserID:    []byte("user1234567890"),
	}
}

func TestChallengeStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()

	handle, err := store.Issue(testSession("abc"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if handle == "" {
		t.Fatal("Issue returned an empty handle")
	}

	session, err := store.Consume(handle)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if session.Challenge != "abc" {
		t.Fatalf("Challenge = %q, want abc", session.Challenge)
	}
}

func TestChallengeIsSingleUse(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()
	handle, err := store.Issue(testSession("abc"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := store.Consume(handle); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	// Replaying a captured finish request must not find the challenge still
	// sitting in the map.
	if _, err := store.Consume(handle); err != ErrUnknownSession {
		t.Fatalf("second Consume error = %v, want ErrUnknownSession", err)
	}
}

func TestChallengeHandlesAreDistinct(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		handle, err := store.Issue(testSession("abc"))
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if seen[handle] {
			t.Fatalf("handle %q was issued twice", handle)
		}
		seen[handle] = true
	}
}

func TestConsumeRejectsUnknownAndEmptyHandles(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()
	if _, err := store.Consume(""); err != ErrUnknownSession {
		t.Fatalf("empty handle error = %v, want ErrUnknownSession", err)
	}
	if _, err := store.Consume("not-a-real-handle"); err != ErrUnknownSession {
		t.Fatalf("unknown handle error = %v, want ErrUnknownSession", err)
	}
}

func TestExpiredChallengeIsRejected(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()
	handle, err := store.Issue(testSession("abc"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Age the entry rather than sleeping for the real TTL.
	store.mu.Lock()
	entry := store.entries[handle]
	entry.expires = time.Now().Add(-time.Second)
	store.entries[handle] = entry
	store.mu.Unlock()

	if _, err := store.Consume(handle); err != ErrUnknownSession {
		t.Fatalf("Consume error = %v, want ErrUnknownSession", err)
	}
}

func TestExpiredChallengesArePrunedOnIssue(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()
	stale, err := store.Issue(testSession("stale"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	store.mu.Lock()
	entry := store.entries[stale]
	entry.expires = time.Now().Add(-time.Second)
	store.entries[stale] = entry
	store.mu.Unlock()

	if _, err := store.Issue(testSession("fresh")); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := store.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1 (the stale entry should have been swept)", got)
	}
}

func TestStoreIsCappedSoUnauthenticatedCallersCannotGrowItForever(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()
	for i := 0; i < maxChallenges+50; i++ {
		if _, err := store.Issue(testSession("abc")); err != nil {
			t.Fatalf("Issue %d: %v", i, err)
		}
	}
	if got := store.Len(); got > maxChallenges {
		t.Fatalf("Len = %d, want at most %d", got, maxChallenges)
	}
}

func TestEvictionKeepsTheStoreUsable(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()
	for i := 0; i < maxChallenges; i++ {
		if _, err := store.Issue(testSession("filler")); err != nil {
			t.Fatalf("Issue: %v", err)
		}
	}
	// A challenge issued once the store is full must still be redeemable, or a
	// flood would deny sign-in to everyone rather than just costing a retry.
	handle, err := store.Issue(testSession("mine"))
	if err != nil {
		t.Fatalf("Issue at capacity: %v", err)
	}
	session, err := store.Consume(handle)
	if err != nil {
		t.Fatalf("Consume at capacity: %v", err)
	}
	if session.Challenge != "mine" {
		t.Fatalf("Challenge = %q, want mine", session.Challenge)
	}
}
