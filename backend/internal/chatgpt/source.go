package chatgpt

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// refreshLeeway is how early a token is treated as spent. Generous on purpose:
// a run can sit in a queue between the middleware reading the token and the
// request reaching OpenAI.
const refreshLeeway = 5 * time.Minute

// Persist writes a rotated token back to the ai_providers row. A function
// rather than a core.App so the refresh path owns nothing of PocketBase.
type Persist func(providerID, oauth string) error

// TokenSource hands out a live access token for one provider row. One per
// provider id, kept in the package registry below rather than in the runtime
// snapshot: config.Runtime rebuilds every client on any ai_providers write, and
// a source rebuilt with it would lose track of a refresh already in flight.
type TokenSource struct {
	providerID string
	persist    Persist
	client     *Client
	logger     *slog.Logger

	// Held across the refresh, which makes concurrent callers wait for one
	// round trip instead of racing to spend the same rotating refresh token.
	mu  sync.Mutex
	tok Token

	// forgotten is set by Forget and never cleared: a source is retired, not
	// paused. Atomic rather than guarded by mu, which is held across the
	// refresh round trip a sign-out must not wait for.
	forgotten atomic.Bool
}

var (
	sourcesMu sync.Mutex
	sources   = map[string]*TokenSource{}
)

// SourceFor returns the source for a provider row, adopting a newer token when
// one has been written elsewhere. An older token is ignored rather than
// adopted: the record may have been read before our own refresh landed, and
// taking it would spend a refresh token that has already rotated.
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

// Forget retires a provider's source. Dropping the registry entry is not
// enough: a refresh already in flight would write the rotated pair to the row,
// which the update hook reads as a sign-in, undoing the sign-out with no error
// anywhere.
func Forget(providerID string) {
	sourcesMu.Lock()
	src := sources[providerID]
	delete(sources, providerID)
	sourcesMu.Unlock()
	if src != nil {
		src.forgotten.Store(true)
	}
}

// withEndpoints repoints the source's auth client. Tests only.
func (s *TokenSource) withEndpoints(e Endpoints) {
	s.client = s.client.WithEndpoints(e)
}

func (s *TokenSource) AccessToken(ctx context.Context) (Token, error) {
	if s.forgotten.Load() {
		return Token{}, ErrNotSignedIn
	}

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
		// look like a sign-out.
		s.logger.Warn("refreshing the ChatGPT token failed",
			"provider_id", s.providerID, slog.Any("error", err))
		if s.tok.Expired(0) {
			return Token{}, err
		}
		return s.tok, nil
	}

	// Carry the identity forward: a refresh response need not repeat the
	// id_token, and the account id is what the billing header names.
	if refreshed.AccountID == "" {
		refreshed.AccountID = s.tok.AccountID
		refreshed.Plan = s.tok.Plan
		refreshed.Email = s.tok.Email
	}
	// Checked again on the way out: the sign-out may have landed while this
	// refresh was on the wire.
	if s.forgotten.Load() {
		return Token{}, ErrNotSignedIn
	}
	s.tok = refreshed
	s.save(refreshed)
	return refreshed, nil
}

func (s *TokenSource) Set(tok Token) error {
	if s.forgotten.Load() {
		return ErrNotSignedIn
	}
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
// mid-request with a working token, and the cost of the miss is one extra
// refresh after the next restart.
func (s *TokenSource) save(tok Token) {
	if s.persist == nil || s.forgotten.Load() {
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
