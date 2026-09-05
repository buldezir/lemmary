package chatgpt

import (
	"context"
	"encoding/json"
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
