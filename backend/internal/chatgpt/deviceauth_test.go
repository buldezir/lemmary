package chatgpt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// authServer stands in for auth.openai.com. Each handler is a field so a test
// can change one leg of the flow without restating the other three.
type authServer struct {
	usercode    http.HandlerFunc
	deviceToken http.HandlerFunc
	oauthToken  http.HandlerFunc
}

func (a authServer) start(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", a.usercode)
	mux.HandleFunc("/api/accounts/deviceauth/token", a.deviceToken)
	mux.HandleFunc("/oauth/token", a.oauthToken)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(srv.Client()).WithEndpoints(Endpoints{
		UserCode:        srv.URL + "/api/accounts/deviceauth/usercode",
		DeviceToken:     srv.URL + "/api/accounts/deviceauth/token",
		OAuthToken:      srv.URL + "/oauth/token",
		VerificationURL: srv.URL + "/codex/device",
		RedirectURI:     srv.URL + "/deviceauth/callback",
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// idToken builds an unsigned JWT with the claim the account id lives in. Only
// the payload segment is ever read, so the header and signature are filler.
func idToken(t *testing.T, accountID, plan, email string) string {
	t.Helper()
	payload := map[string]any{
		"email": email,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  plan,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"
}

// The whole point of the device flow: no redirect listener, so a headless
// server can complete a sign-in the operator approves from their own browser.
func TestDeviceLoginCompletesWithoutARedirect(t *testing.T) {
	t.Parallel()
	approved := false
	client := authServer{
		usercode: func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != ClientID {
				t.Errorf("client_id = %q, want the Codex client", body["client_id"])
			}
			writeJSON(w, 200, map[string]any{
				"device_auth_id": "dev-1", "user_code": "ABCD-1234", "interval": 2,
			})
		},
		deviceToken: func(w http.ResponseWriter, r *http.Request) {
			if !approved {
				// The endpoint answers 200 with nothing while it waits.
				writeJSON(w, 200, map[string]any{})
				return
			}
			writeJSON(w, 200, map[string]any{
				"authorization_code": "auth-code", "code_verifier": "verifier",
			})
		},
		oauthToken: func(w http.ResponseWriter, r *http.Request) {
			// The code grant goes out form-encoded, against the issuer's own
			// device callback -- the value the code was issued for.
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code_verifier") != "verifier" {
				t.Errorf("exchange body = %v", r.PostForm)
			}
			if !strings.HasSuffix(r.PostForm.Get("redirect_uri"), "/deviceauth/callback") {
				t.Errorf("redirect_uri = %q", r.PostForm.Get("redirect_uri"))
			}
			writeJSON(w, 200, map[string]any{
				"access_token":  "access-1",
				"refresh_token": "refresh-1",
				"id_token":      idToken(t, "acct-9", "pro", "someone@example.com"),
				"expires_in":    3600,
			})
		},
	}.start(t)

	ctx := context.Background()
	pending, err := client.StartDeviceLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending.UserCode != "ABCD-1234" || pending.Interval != 2*time.Second {
		t.Fatalf("pending = %+v", pending)
	}

	if _, err := client.PollDeviceLogin(ctx, pending); !errors.Is(err, ErrAuthPending) {
		t.Fatalf("before approval: err = %v, want ErrAuthPending", err)
	}

	approved = true
	tok, err := client.PollDeviceLogin(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "access-1" || tok.Refresh != "refresh-1" {
		t.Fatalf("token = %+v", tok)
	}
	// The account id is what the inference request's chatgpt-account-id header
	// carries; losing it here would fail every later request with a 4xx.
	if tok.AccountID != "acct-9" || tok.Plan != "pro" || tok.Email != "someone@example.com" {
		t.Fatalf("identity = %+v", tok)
	}
	if !tok.Valid() || tok.Expired(0) {
		t.Fatalf("fresh token reads as unusable: %+v", tok)
	}
}

// Device-code sign-in is off by default on every account, so this is the first
// thing most operators hit. It has to arrive as advice, not as a bare 4xx.
func TestDisabledDeviceAuthIsNamed(t *testing.T) {
	t.Parallel()
	client := authServer{
		usercode: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 403, map[string]any{"error": "device_code_disabled"})
		},
		deviceToken: func(w http.ResponseWriter, r *http.Request) {},
		oauthToken:  func(w http.ResponseWriter, r *http.Request) {},
	}.start(t)

	_, err := client.StartDeviceLogin(context.Background())
	if !errors.Is(err, ErrDeviceAuthDisabled) {
		t.Fatalf("err = %v, want ErrDeviceAuthDisabled", err)
	}
	if !strings.Contains(err.Error(), "Security") {
		t.Errorf("the message does not say where to turn it on: %v", err)
	}
}

func TestExpiredCodeIsNotRetried(t *testing.T) {
	t.Parallel()
	client := authServer{
		usercode: func(w http.ResponseWriter, r *http.Request) {},
		deviceToken: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 400, map[string]any{"error": "expired_token"})
		},
		oauthToken: func(w http.ResponseWriter, r *http.Request) {},
	}.start(t)

	pending := &Pending{DeviceAuthID: "dev-1", UserCode: "ABCD", ExpiresAt: time.Now().Add(time.Minute)}
	if _, err := client.PollDeviceLogin(context.Background(), pending); !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("err = %v, want ErrAuthExpired", err)
	}

	// A code whose fifteen minutes ran out locally never reaches the network.
	stale := &Pending{DeviceAuthID: "dev-1", UserCode: "ABCD", ExpiresAt: time.Now().Add(-time.Second)}
	if _, err := client.PollDeviceLogin(context.Background(), stale); !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("stale: err = %v, want ErrAuthExpired", err)
	}
}

// The refresh token rotates on use, but the issuer does not always send a new
// one. Dropping it in that case would sign the account out at the next restart.
func TestRefreshKeepsTheOldRefreshTokenWhenNoneIsReturned(t *testing.T) {
	t.Parallel()
	client := authServer{
		usercode:    func(w http.ResponseWriter, r *http.Request) {},
		deviceToken: func(w http.ResponseWriter, r *http.Request) {},
		oauthToken: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"access_token": "access-2", "expires_in": 3600})
		},
	}.start(t)

	tok, err := client.RefreshToken(context.Background(), "refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Refresh != "refresh-1" {
		t.Fatalf("refresh token = %q, want the one we sent", tok.Refresh)
	}
}

func TestTokenRoundTripsThroughTheColumn(t *testing.T) {
	t.Parallel()
	// An empty column is a row nobody has signed in to, not a broken one.
	if tok, err := ParseToken("  "); err != nil || tok.Valid() {
		t.Fatalf("empty column: %+v, %v", tok, err)
	}
	if _, err := ParseToken("not json"); err == nil {
		t.Fatal("unreadable column parsed without complaint")
	}

	in := Token{Access: "a", Refresh: "r", ExpiresAt: time.Now().Add(time.Hour).Round(time.Second), AccountID: "acct"}
	raw, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Access != in.Access || out.Refresh != in.Refresh || !out.ExpiresAt.Equal(in.ExpiresAt) {
		t.Fatalf("round trip lost something: %+v -> %+v", in, out)
	}
}

// The issuer quotes interval and expires_in as strings on some responses, and
// a sign-in that dies on the poll hint is a sign-in lost to nothing.
func TestDeviceLoginAcceptsQuotedNumbers(t *testing.T) {
	t.Parallel()
	client := authServer{
		usercode: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{
				"device_auth_id": "dev-1", "user_code": "ABCD-1234", "interval": "5",
			})
		},
		deviceToken: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{
				"authorization_code": "auth-code", "code_verifier": "verifier",
			})
		},
		oauthToken: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{
				"access_token": "access-1", "refresh_token": "refresh-1",
				"id_token":   idToken(t, "acct-9", "pro", "someone@example.com"),
				"expires_in": "900",
			})
		},
	}.start(t)

	ctx := context.Background()
	pending, err := client.StartDeviceLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Interval != 5*time.Second {
		t.Fatalf("interval = %v, want 5s", pending.Interval)
	}
	tok, err := client.PollDeviceLogin(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Until(tok.ExpiresAt).Round(time.Minute); got != 15*time.Minute {
		t.Fatalf("expires in %v, want 15m", got)
	}
}

// The poll endpoint answers 403 or 404 for the whole time the operator is in
// the browser. Reading either as a failure ends every sign-in on its first try.
func TestPollReadsARefusalAsWaiting(t *testing.T) {
	t.Parallel()
	replies := []int{http.StatusForbidden, http.StatusNotFound}
	attempt := 0
	client := authServer{
		usercode: func(w http.ResponseWriter, r *http.Request) {},
		deviceToken: func(w http.ResponseWriter, r *http.Request) {
			if attempt < len(replies) {
				writeJSON(w, replies[attempt], map[string]any{"detail": "not found"})
				attempt++
				return
			}
			writeJSON(w, 200, map[string]any{
				"authorization_code": "auth-code", "code_verifier": "verifier",
			})
		},
		oauthToken: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"access_token": "access-1", "refresh_token": "refresh-1"})
		},
	}.start(t)

	ctx := context.Background()
	pending := &Pending{DeviceAuthID: "dev-1", UserCode: "ABCD", ExpiresAt: time.Now().Add(time.Minute)}
	for range replies {
		if _, err := client.PollDeviceLogin(ctx, pending); !errors.Is(err, ErrAuthPending) {
			t.Fatalf("err = %v, want ErrAuthPending", err)
		}
	}
	if _, err := client.PollDeviceLogin(ctx, pending); err != nil {
		t.Fatal(err)
	}
}

// The refresh grant answers with no expires_in, so the access token's own exp
// claim is the only honest answer. Guessing an hour against a shorter lifetime
// is a stack of 401s nobody refreshes out of.
func TestRefreshTakesExpiryFromTheAccessToken(t *testing.T) {
	t.Parallel()
	exp := time.Now().Add(20 * time.Minute).Truncate(time.Second)
	client := authServer{
		usercode:    func(w http.ResponseWriter, r *http.Request) {},
		deviceToken: func(w http.ResponseWriter, r *http.Request) {},
		oauthToken: func(w http.ResponseWriter, r *http.Request) {
			// A refresh goes out as JSON, unlike the code grant.
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["grant_type"] != "refresh_token" || body["refresh_token"] != "refresh-1" {
				t.Errorf("refresh body = %v", body)
			}
			writeJSON(w, 200, map[string]any{"access_token": jwtWithExp(t, exp)})
		},
	}.start(t)

	tok, err := client.RefreshToken(context.Background(), "refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	if !tok.ExpiresAt.Equal(exp) {
		t.Fatalf("expires at %v, want the exp claim %v", tok.ExpiresAt, exp)
	}
}

// jwtWithExp builds an unsigned access token carrying only an exp claim.
func jwtWithExp(t *testing.T, exp time.Time) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"exp": exp.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"
}
