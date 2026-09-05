package chatgpt

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// refreshLeeway is how early a token is treated as spent. Generous on purpose:
// an extraction run can sit in a queue for a while between the moment the
// middleware reads the token and the moment the request reaches OpenAI.
const refreshLeeway = 5 * time.Minute

// Persist writes a rotated token back to the ai_providers row.
//
// A function rather than a core.App here so the refresh path owns nothing of
// PocketBase: the appapi and config packages already hold the app, and this one
// stays testable without a database.
type Persist func(providerID, oauth string) error

// TokenSource hands out a live access token for one provider row, refreshing it
// when it is close to expiring.
//
// One per provider id, kept in the package registry below rather than in the
// runtime snapshot. That is deliberate: config.Runtime rebuilds every client on
// any ai_providers write, and a source rebuilt with it would lose track of a
// refresh already in flight -- and would re-read a row this very source is
// about to update.
type TokenSource struct {
	providerID string
	persist    Persist
	client     *Client
	logger     *slog.Logger

	// Held across the refresh, which makes concurrent callers wait for one
	// round trip instead of racing to spend the same rotating refresh token.
	mu  sync.Mutex
	tok Token
}

var (
	sourcesMu sync.Mutex
	sources   = map[string]*TokenSource{}
)

// SourceFor returns the source for a provider row, creating it on first use and
// adopting a newer token when one has been written elsewhere -- a fresh
// sign-in, or another process's refresh.
//
// An older token is ignored rather than adopted: the record we were handed may
// have been read before our own refresh landed, and taking it would spend a
// refresh token that has already rotated.
func SourceFor(providerID, oauth string, persist Persist, logger *slog.Logger) *TokenSource {
	if logger == nil {
		logger = slog.Default()
	}
	incoming, err := ParseToken(oauth)
	if err != nil {
		logger.Warn("stored ChatGPT token is unreadable; sign in again",
			"provider_id", providerID, slog.Any("error", err))
	}

	sourcesMu.Lock()
	defer sourcesMu.Unlock()

	src, ok := sources[providerID]
	if !ok {
		src = &TokenSource{
			providerID: providerID,
			persist:    persist,
			client:     NewClient(nil),
			logger:     logger,
		}
		src.tok = incoming
		sources[providerID] = src
		return src
	}

	src.mu.Lock()
	if !src.tok.Valid() || incoming.ExpiresAt.After(src.tok.ExpiresAt) {
		src.tok = incoming
	}
	src.mu.Unlock()
	src.persist = persist
	return src
}

// Forget drops a provider's source, for sign-out and deletion. Without it a
// row that signed out would keep serving from the token still in memory.
func Forget(providerID string) {
	sourcesMu.Lock()
	delete(sources, providerID)
	sourcesMu.Unlock()
}

// withEndpoints repoints the source's auth client. Tests only.
func (s *TokenSource) withEndpoints(e Endpoints) *TokenSource {
	s.client = s.client.WithEndpoints(e)
	return s
}

// Identity is what Settings shows about the signed-in account. Never the token.
func (s *TokenSource) Identity() (accountID, plan, email string, signedIn bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok.AccountID, s.tok.Plan, s.tok.Email, s.tok.Valid()
}

// AccessToken returns a token good for the next few minutes, refreshing first
// if the one in hand is not.
func (s *TokenSource) AccessToken(ctx context.Context) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.tok.Valid() {
		return Token{}, ErrNotSignedIn
	}
	if !s.tok.Expired(refreshLeeway) {
		return s.tok, nil
	}

	refreshed, err := s.client.RefreshToken(ctx, s.tok.Refresh)
	if err != nil {
		// Keep the old token rather than clearing it: a network blip must not
		// look like a sign-out, and the access token may still have minutes
		// left inside the leeway.
		s.logger.Warn("refreshing the ChatGPT token failed",
			"provider_id", s.providerID, slog.Any("error", err))
		if s.tok.Expired(0) {
			return Token{}, err
		}
		return s.tok, nil
	}

	// Carry the identity forward: a refresh response need not repeat the
	// id_token, and losing the account id would break the request header that
	// says which ChatGPT account is being billed.
	if refreshed.AccountID == "" {
		refreshed.AccountID = s.tok.AccountID
		refreshed.Plan = s.tok.Plan
		refreshed.Email = s.tok.Email
	}
	s.tok = refreshed
	s.save(refreshed)
	return refreshed, nil
}

// Set replaces the token after a completed sign-in and stores it.
func (s *TokenSource) Set(tok Token) error {
	s.mu.Lock()
	s.tok = tok
	s.mu.Unlock()

	raw, err := tok.Marshal()
	if err != nil {
		return err
	}
	if s.persist == nil {
		return nil
	}
	return s.persist(s.providerID, raw)
}

// save persists a rotation. Failures are logged, not returned: the caller is
// mid-request with a working token, and refusing to answer because the write
// failed would turn a recoverable database hiccup into a failed extraction.
// The cost of the miss is one extra refresh after the next restart.
func (s *TokenSource) save(tok Token) {
	if s.persist == nil {
		return
	}
	raw, err := tok.Marshal()
	if err == nil {
		err = s.persist(s.providerID, raw)
	}
	if err != nil {
		s.logger.Warn("storing the refreshed ChatGPT token failed",
			"provider_id", s.providerID, slog.Any("error", err))
	}
}
