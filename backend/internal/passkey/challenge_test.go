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
	for i := 0; i < maxChallenges; i++ {
		if _, err := store.Issue(testSession("abc")); err != nil {
			t.Fatalf("Issue %d: %v", i, err)
		}
	}
	if _, err := store.Issue(testSession("one too many")); err != ErrTooManyChallenges {
		t.Fatalf("error = %v, want ErrTooManyChallenges", err)
	}
	if got := store.Len(); got > maxChallenges {
		t.Fatalf("Len = %d, want at most %d", got, maxChallenges)
	}
}

func TestFloodCannotInvalidateACeremonyAlreadyUnderWay(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()

	// A victim starts a ceremony, then an unauthenticated flood fills the store.
	victim, err := store.Issue(testSession("victim"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	refused := 0
	for i := 0; i < maxChallenges*2; i++ {
		if _, err := store.Issue(testSession("flood")); err == ErrTooManyChallenges {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("the flood should have been refused once the store filled")
	}

	// The victim's handle must still redeem. Evicting the oldest live entry to
	// make room would have destroyed a ceremony already under way, which is why
	// the store refuses the newcomer instead.
	session, err := store.Consume(victim)
	if err != nil {
		t.Fatalf("the victim's in-flight challenge was lost: %v", err)
	}
	if session.Challenge != "victim" {
		t.Fatalf("Challenge = %q, want victim", session.Challenge)
	}
}

func TestCapacityIsReleasedAsChallengesExpire(t *testing.T) {
	t.Parallel()
	store := NewChallengeStore()
	for i := 0; i < maxChallenges; i++ {
		if _, err := store.Issue(testSession("filler")); err != nil {
			t.Fatalf("Issue: %v", err)
		}
	}
	if _, err := store.Issue(testSession("blocked")); err != ErrTooManyChallenges {
		t.Fatalf("error = %v, want ErrTooManyChallenges", err)
	}

	// A flood costs the attacker nothing but buys them only the TTL: once the
	// entries age out the store serves normally again.
	store.mu.Lock()
	for handle, entry := range store.entries {
		entry.expires = time.Now().Add(-time.Second)
		store.entries[handle] = entry
	}
	store.mu.Unlock()

	handle, err := store.Issue(testSession("after expiry"))
	if err != nil {
		t.Fatalf("Issue after expiry: %v", err)
	}
	if _, err := store.Consume(handle); err != nil {
		t.Fatalf("Consume after expiry: %v", err)
	}
}
