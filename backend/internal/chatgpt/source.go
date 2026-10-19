package chatgpt

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
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

// signOut clears the row of a token the issuer will no longer refresh, so Admin
// offers the sign-in again instead of every request failing on the same 401.
// Unregistered before the write, like Forget: the save reloads the runtime,
// whose SourceFor would otherwise wait on the lock this source holds. A source
// already retired leaves the row alone, since a fresh sign-in may own it now.
func (s *TokenSource) signOut() {
	if s.forgotten.Swap(true) {
		return
	}
	sourcesMu.Lock()
	if sources[s.providerID] == s {
		delete(sources, s.providerID)
	}
	sourcesMu.Unlock()
	if s.persist == nil {
		return
	}
	if err := s.persist(s.providerID, ""); err != nil {
		s.logger.Warn("clearing the refused ChatGPT token failed",
			"provider_id", s.providerID, slog.Any("error", err))
	}
}

// RefreshAll lets every registered source refresh a token that is due. They
// otherwise refresh only when a request needs one, and an idle instance would
// let its refresh token lapse. Failures are logged by AccessToken.
//
// ponytail: covers providers some binding uses; walk the ai_providers rows if
// an unbound sign-in ever needs keeping alive too.
func RefreshAll(ctx context.Context) {
	sourcesMu.Lock()
	all := slices.Collect(maps.Values(sources))
	sourcesMu.Unlock()
	for _, src := range all {
		_, _ = src.AccessToken(ctx)
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

	// Detached from the caller: the issuer rotates the pair as it answers, so a
	// request cancelled mid-refresh would drop the only copy of the new refresh
	// token, and spending the old one again gets the whole sign-in revoked.
	refreshed, err := s.client.RefreshToken(context.WithoutCancel(ctx), s.tok.Refresh)
	if errors.Is(err, ErrSignInRevoked) {
		s.logger.Warn("the ChatGPT sign-in was refused; signing the provider out",
			"provider_id", s.providerID, slog.Any("error", err))
		s.tok = Token{}
		s.signOut()
		return Token{}, err
	}
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
