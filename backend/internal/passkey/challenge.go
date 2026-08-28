package passkey

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// ErrUnknownSession is returned when a finish request presents a handle that was
// never issued, has already been used, or has expired.
var ErrUnknownSession = errors.New("passkey challenge expired or already used")

// ErrTooManyChallenges means the store is full of live challenges. Callers render
// it as 429 rather than 500: nothing is broken, the server is shedding load.
var ErrTooManyChallenges = errors.New("too many passkey ceremonies in progress")

const (
	// challengeTTL bounds how long a user has between the button click and the
	// authenticator prompt being answered. Long enough for a fingerprint reader
	// or a phone handoff, short enough that an abandoned attempt clears itself.
	challengeTTL = 5 * time.Minute

	// maxChallenges caps the store. login/begin is unauthenticated by necessity —
	// the whole point is that the caller has no session yet — and this app
	// configures no rate limiter, so without a cap anyone could grow the map
	// until the process ran out of memory.
	//
	// At capacity a new challenge is refused rather than made room for. Evicting
	// the oldest live entry was the first design and it was worse: an
	// unauthenticated caller could push entries until a victim's in-flight handle
	// fell off the end, invalidating a ceremony the victim had already started and
	// could otherwise have completed. Refusing instead means a flood can stop new
	// ceremonies beginning — the same denial either way — but can never destroy one
	// that is already under way, and the attacker's own entries drain within the
	// TTL. Failing closed on a bounded resource beats sacrificing valid state.
	//
	// Per-IP throttling is deliberately not attempted here. PocketBase's
	// e.RealIP() honours its TrustedProxy setting, which this app leaves unset, so
	// behind a reverse proxy every request reports the proxy's address: a per-IP
	// bucket would either do nothing or lock out every user at once. The operator's
	// tool is PocketBase's own rate limiter (Admin → Settings → Rate limits), which
	// composes correctly with TrustedProxy once that is configured.
	maxChallenges = 4096
)

// ChallengeStore holds webauthn.SessionData between the begin and finish halves
// of a ceremony, keyed by an opaque handle the client echoes back.
//
// In-process rather than in the database. The session data is worthless once
// consumed, lives for five minutes, and is only ever read by the same process
// that wrote it: this app is a single container over SQLite, so there are no
// replicas to share it with, and a restart mid-ceremony costs one retry. A
// collection would add a table, a write on every login attempt including the
// failed ones, and a sweeper, to buy durability that nothing here wants.
//
// An httpOnly cookie was the other candidate — it is what the go-webauthn
// examples suggest — and was rejected because the entire app API is bearer-token
// and cookieless. Introducing one cookie for one flow would drag SameSite,
// path scoping and reverse-proxy behaviour into a feature that does not need them.
type ChallengeStore struct {
	mu      sync.Mutex
	entries map[string]challengeEntry
}

type challengeEntry struct {
	session webauthn.SessionData
	expires time.Time
}

// NewChallengeStore returns an empty store.
func NewChallengeStore() *ChallengeStore {
	return &ChallengeStore{entries: map[string]challengeEntry{}}
}

// Issue stores session data and returns the handle the client must send back.
// Returns ErrTooManyChallenges when the store is full of live entries.
func (s *ChallengeStore) Issue(session *webauthn.SessionData) (string, error) {
	handle, err := newHandle()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	if len(s.entries) >= maxChallenges {
		return "", ErrTooManyChallenges
	}
	s.entries[handle] = challengeEntry{
		session: *session,
		expires: time.Now().Add(challengeTTL),
	}
	return handle, nil
}

// Consume returns the session data for a handle and removes it. A challenge is
// single-use: replaying a captured finish request must not re-verify against a
// challenge that is still sitting in the map.
func (s *ChallengeStore) Consume(handle string) (webauthn.SessionData, error) {
	if handle == "" {
		return webauthn.SessionData{}, ErrUnknownSession
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[handle]
	delete(s.entries, handle)
	if !ok || time.Now().After(entry.expires) {
		return webauthn.SessionData{}, ErrUnknownSession
	}
	return entry.session, nil
}

// Len reports how many live challenges are held. For tests.
func (s *ChallengeStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	return len(s.entries)
}

// pruneLocked drops expired entries. Sweeping on write rather than from a
// goroutine keeps the store free of background machinery; the map only grows on
// a begin request, so that is exactly when it needs tidying.
func (s *ChallengeStore) pruneLocked(now time.Time) {
	for handle, entry := range s.entries {
		if now.After(entry.expires) {
			delete(s.entries, handle)
		}
	}
}

func newHandle() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
