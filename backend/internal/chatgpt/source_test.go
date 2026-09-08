package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// refreshingSource builds a source wired to a stub /oauth/token, and reports
// how many times that endpoint was called.
func refreshingSource(t *testing.T, tok Token, persist Persist) (*TokenSource, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-" + body["refresh_token"],
			"refresh_token": "rotated",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(srv.Close)

	Forget("p1")
	t.Cleanup(func() { Forget("p1") })

	raw, err := tok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	src := SourceFor("p1", raw, persist, nil)
	src.withEndpoints(Endpoints{OAuthToken: srv.URL})
	return src, &calls
}

func TestAccessTokenRefreshesBeforeExpiryAndPersistsTheRotation(t *testing.T) {
	var mu sync.Mutex
	var stored string
	persist := func(_, oauth string) error {
		mu.Lock()
		defer mu.Unlock()
		stored = oauth
		return nil
	}

	// Inside the leeway: still technically valid, but too close to spend on a
	// request that may sit in a queue first.
	src, calls := refreshingSource(t, Token{
		Access: "old", Refresh: "refresh-1", ExpiresAt: time.Now().Add(time.Minute),
	}, persist)

	tok, err := src.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "access-refresh-1" {
		t.Fatalf("access token = %q, want the refreshed one", tok.Access)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}

	mu.Lock()
	saved := stored
	mu.Unlock()
	if saved == "" {
		t.Fatal("the rotated refresh token was not written back")
	}
	parsed, err := ParseToken(saved)
	if err != nil {
		t.Fatal(err)
	}
	// The refresh token rotates on use: storing the old one would mean the next
	// refresh spends a credential the issuer has already retired.
	if parsed.Refresh != "rotated" {
		t.Fatalf("stored refresh token = %q, want the rotated one", parsed.Refresh)
	}

	// A token with an hour left is handed out as-is.
	if _, err := src.AccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d after a second read, want 1", calls.Load())
	}
}

// Concurrent requests must not each spend the rotating refresh token.
func TestConcurrentCallersRefreshOnce(t *testing.T) {
	src, calls := refreshingSource(t, Token{
		Access: "old", Refresh: "refresh-1", ExpiresAt: time.Now().Add(time.Minute),
	}, nil)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := src.AccessToken(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}
}

// A network blip is not a sign-out: the access token may still have minutes
// left inside the leeway, and clearing it would break a working install.
func TestAFailedRefreshKeepsAStillValidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	Forget("p2")
	defer Forget("p2")
	raw, err := Token{Access: "old", Refresh: "r", ExpiresAt: time.Now().Add(time.Minute)}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	src := SourceFor("p2", raw, nil, nil)
	src.withEndpoints(Endpoints{OAuthToken: srv.URL})

	tok, err := src.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want the old token back", err)
	}
	if tok.Access != "old" {
		t.Fatalf("access token = %q, want the one we still had", tok.Access)
	}
}

func TestAnUnsignedProviderSaysSo(t *testing.T) {
	Forget("p3")
	defer Forget("p3")
	src := SourceFor("p3", "", nil, nil)
	if _, err := src.AccessToken(context.Background()); err != ErrNotSignedIn {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
	}
}

// The registry is what survives a runtime rebuild. A row re-read from the
// database mid-refresh must not roll the source back to the token it held
// before -- that credential has already been spent.
func TestSourceForIgnoresAnOlderToken(t *testing.T) {
	Forget("p4")
	defer Forget("p4")

	newer := Token{Access: "new", Refresh: "r2", ExpiresAt: time.Now().Add(time.Hour)}
	raw, err := newer.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	src := SourceFor("p4", raw, nil, nil)

	older, err := Token{Access: "old", Refresh: "r1", ExpiresAt: time.Now().Add(time.Minute)}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again := SourceFor("p4", older, nil, nil)
	if again != src {
		t.Fatal("SourceFor built a second source for one provider")
	}
	tok, err := src.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "new" {
		t.Fatalf("access token = %q, want the newer one", tok.Access)
	}
}

// A sign-out is two writes: the row's token is cleared, and the source is
// retired. Only the second reaches a refresh that is already on the wire -- and
// without it that refresh comes back, stores its rotated pair, and the provider
// update hook reads the write as a fresh sign-in and rebuilds signed-in
// clients. The sign-out undoes itself, with no error anywhere to say so.
func TestARefreshInFlightAcrossForgetStoresNothing(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-new",
			"refresh_token": "rotated",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	var stores atomic.Int32
	persist := func(_, _ string) error {
		stores.Add(1)
		return nil
	}

	const id = "p-signout"
	Forget(id)
	t.Cleanup(func() { Forget(id) })
	// Inside the leeway, so the very next AccessToken refreshes.
	raw, err := Token{Access: "old", Refresh: "refresh-1", ExpiresAt: time.Now().Add(time.Minute)}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	src := SourceFor(id, raw, persist, nil)
	src.withEndpoints(Endpoints{OAuthToken: srv.URL})

	type result struct {
		tok Token
		err error
	}
	done := make(chan result, 1)
	go func() {
		tok, err := src.AccessToken(context.Background())
		done <- result{tok, err}
	}()

	<-started
	// The sign-out lands while the refresh is still in the air. It must not
	// block on it either: mu is held across that round trip, which is why the
	// flag is atomic rather than guarded by the mutex.
	Forget(id)
	close(release)

	got := <-done
	if !errors.Is(got.err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn: a retired source must serve nothing", got.err)
	}
	if got.tok.Access != "" {
		t.Errorf("handed out %q after sign-out", got.tok.Access)
	}
	if n := stores.Load(); n != 0 {
		t.Fatalf("persisted %d times after sign-out; the sign-out would be undone", n)
	}
}

func TestAForgottenSourceHandsOutNoToken(t *testing.T) {
	const id = "p-retired"
	Forget(id)
	t.Cleanup(func() { Forget(id) })
	raw, err := Token{Access: "live", Refresh: "r", ExpiresAt: time.Now().Add(time.Hour)}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	src := SourceFor(id, raw, nil, nil)

	// Good for an hour, so nothing here needs the network.
	if _, err := src.AccessToken(context.Background()); err != nil {
		t.Fatalf("before sign-out: %v", err)
	}
	Forget(id)
	if _, err := src.AccessToken(context.Background()); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("after sign-out: err = %v, want ErrNotSignedIn", err)
	}
}

// Set is the sign-in write. A source retired first must refuse it rather than
// store a token against a row that is being signed out or deleted.
func TestSetOnAForgottenSourceStoresNothing(t *testing.T) {
	const id = "p-set"
	Forget(id)
	t.Cleanup(func() { Forget(id) })

	var stores atomic.Int32
	src := SourceFor(id, "", func(_, _ string) error {
		stores.Add(1)
		return nil
	}, nil)

	Forget(id)
	err := src.Set(Token{Access: "a", Refresh: "r", ExpiresAt: time.Now().Add(time.Hour)})
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
	}
	if n := stores.Load(); n != 0 {
		t.Fatalf("persisted %d times", n)
	}
}

// Forget drops the registry entry as well as retiring the source, so the next
// SourceFor builds a working one rather than handing back the retired object.
func TestSourceForAfterForgetBuildsALiveSource(t *testing.T) {
	const id = "p-resign"
	Forget(id)
	t.Cleanup(func() { Forget(id) })

	first := SourceFor(id, "", nil, nil)
	Forget(id)

	raw, err := Token{Access: "fresh", Refresh: "r2", ExpiresAt: time.Now().Add(time.Hour)}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	second := SourceFor(id, raw, nil, nil)
	if second == first {
		t.Fatal("SourceFor handed back the retired source")
	}
	tok, err := second.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("the new source refuses to serve: %v", err)
	}
	if tok.Access != "fresh" {
		t.Errorf("access = %q", tok.Access)
	}
}
