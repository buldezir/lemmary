package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClientID is the Codex client OpenAI's own CLI and IDE extensions present.
//
// There is no second option. The device-code endpoints below only issue tokens
// for OpenAI's registered clients, and the Codex backend only answers requests
// whose originator header names one -- see codexOriginator in transport.go.
// Using them means presenting ourselves as that client, which is the whole
// reason this SDK ships behind a flag and never runs on a managed instance.
const ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// Endpoints are OpenAI's device-code and token URLs.
//
// A struct rather than constants so tests can point the whole flow at an
// httptest server. Production callers take DefaultEndpoints and never touch it.
type Endpoints struct {
	UserCode        string
	DeviceToken     string
	OAuthToken      string
	VerificationURL string

	// RedirectURI is the value the authorization code was issued against. The
	// device flow never redirects anywhere, but the exchange still checks the
	// parameter, so it has to be the issuer's own device callback -- not the
	// loopback address the browser flow registers.
	RedirectURI string
}

func DefaultEndpoints() Endpoints {
	return Endpoints{
		UserCode:        "https://auth.openai.com/api/accounts/deviceauth/usercode",
		DeviceToken:     "https://auth.openai.com/api/accounts/deviceauth/token",
		OAuthToken:      "https://auth.openai.com/oauth/token",
		VerificationURL: "https://auth.openai.com/codex/device",
		RedirectURI:     "https://auth.openai.com/deviceauth/callback",
	}
}

// authEndpoints is what the sign-in handlers reach. DefaultEndpoints in every
// deployment; the end-to-end suite points it at a stub auth host, which is the
// only way to drive the device flow without OpenAI's own and a human in a
// second browser tab.
//
// A package variable, and deliberately not an environment variable or a
// parameter threaded through Register. An env variable would be a shippable
// setting that redirects an OAuth flow to an arbitrary host, which is a
// configuration mistake worth making impossible; a parameter would carry a
// test seam through appwire and main for the sake of one suite. This is
// reachable only from Go code linked into the same binary.
var authEndpoints = DefaultEndpoints()

// AuthEndpoints returns the endpoints the sign-in handlers use.
func AuthEndpoints() Endpoints { return authEndpoints }

// SetAuthEndpointsForTesting repoints the sign-in flow and returns a function
// that puts it back. Never called outside a test binary.
func SetAuthEndpointsForTesting(e Endpoints) func() {
	previous := authEndpoints
	authEndpoints = e
	return func() { authEndpoints = previous }
}

var (
	// ErrAuthPending is the operator not having finished in the browser yet.
	// Expected, and the reason polling exists; never surfaced as a failure.
	ErrAuthPending = errors.New("chatgpt: authorization pending")

	// ErrAuthExpired is the user code timing out, roughly fifteen minutes after
	// it was issued. The only cure is a fresh code.
	ErrAuthExpired = errors.New("chatgpt: device code expired")

	// ErrDeviceAuthDisabled is the account not permitting device-code sign-in.
	// It is off by default for everyone, so this is the first thing most
	// operators will hit; the message names the setting.
	ErrDeviceAuthDisabled = errors.New("chatgpt: device code sign-in is not enabled for this account -- turn it on under ChatGPT Settings -> Security, or ask a workspace admin to grant it")

	// ErrNotSignedIn is a provider row with no token in it.
	ErrNotSignedIn = errors.New("chatgpt: provider is not signed in")
)

const (
	deviceCodeTTL     = 15 * time.Minute
	defaultPollPeriod = 5 * time.Second
	authTimeout       = 30 * time.Second
)

// Client talks to OpenAI's auth host. Zero value is not usable; use NewClient.
type Client struct {
	http      *http.Client
	endpoints Endpoints
}

func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: authTimeout}
	}
	return &Client{http: hc, endpoints: DefaultEndpoints()}
}

// WithEndpoints returns a copy pointed at other URLs. Tests only.
func (c *Client) WithEndpoints(e Endpoints) *Client {
	return &Client{http: c.http, endpoints: e}
}

// Pending is a device-code login waiting on the operator's browser.
//
// It never leaves the server: DeviceAuthID is the half that would let anyone
// holding it complete the sign-in, so the API hands the browser only the user
// code and the URL and keeps this in memory. See the pending registry in
// appapi.
type Pending struct {
	DeviceAuthID    string
	UserCode        string
	VerificationURL string
	Interval        time.Duration
	ExpiresAt       time.Time
}

func (p *Pending) Expired() bool { return time.Now().After(p.ExpiresAt) }

// flexInt is a whole number that the issuer sometimes quotes as a string.
//
// Both spellings show up in the device-code responses -- "interval": 5 and
// "interval": "5" -- and a plain int rejects the second, which failed the
// whole sign-in over a field we only use as a hint.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	text := strings.TrimSpace(string(b))
	if text == "null" || text == "" {
		return nil
	}
	if unquoted, err := strconv.Unquote(text); err == nil {
		text = strings.TrimSpace(unquoted)
		if text == "" {
			return nil
		}
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("chatgpt: %q is not a number", text)
	}
	*f = flexInt(n)
	return nil
}

// StartDeviceLogin asks for a user code.
func (c *Client) StartDeviceLogin(ctx context.Context) (*Pending, error) {
	var out struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		// Older responses spell it without the underscore. Cheaper to accept
		// both than to discover which one a given deployment sends.
		UserCodeAlt string  `json:"usercode"`
		Interval    flexInt `json:"interval"`
	}
	body := map[string]string{"client_id": ClientID}
	if err := c.postJSON(ctx, c.endpoints.UserCode, body, &out); err != nil {
		// Asking for a code at all is what the account setting gates, so a
		// refusal here is that setting rather than anything about this request.
		if status := statusOf(err); status == http.StatusForbidden || status == http.StatusNotFound {
			return nil, ErrDeviceAuthDisabled
		}
		return nil, err
	}

	code := strings.TrimSpace(out.UserCode)
	if code == "" {
		code = strings.TrimSpace(out.UserCodeAlt)
	}
	if strings.TrimSpace(out.DeviceAuthID) == "" || code == "" {
		return nil, fmt.Errorf("chatgpt: device authorization response is missing a code")
	}

	interval := time.Duration(out.Interval) * time.Second
	if interval <= 0 {
		interval = defaultPollPeriod
	}
	return &Pending{
		DeviceAuthID:    out.DeviceAuthID,
		UserCode:        code,
		VerificationURL: c.endpoints.VerificationURL,
		Interval:        interval,
		ExpiresAt:       time.Now().Add(deviceCodeTTL),
	}, nil
}

// PollDeviceLogin checks once whether the operator has approved the code, and
// exchanges the authorization code for a token pair if they have.
//
// One attempt per call, with ErrAuthPending for "not yet": the caller owns the
// interval, which lets the browser drive it through the API instead of tying up
// a request for the full fifteen minutes.
func (c *Client) PollDeviceLogin(ctx context.Context, p *Pending) (Token, error) {
	if p == nil {
		return Token{}, ErrAuthExpired
	}
	if p.Expired() {
		return Token{}, ErrAuthExpired
	}

	var out struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	body := map[string]string{
		"device_auth_id": p.DeviceAuthID,
		"user_code":      p.UserCode,
	}
	if err := c.postJSON(ctx, c.endpoints.DeviceToken, body, &out); err != nil {
		// The endpoint answers 403 or 404 for the whole fifteen minutes the
		// operator is still in the browser. Reading either as a failure would
		// end every sign-in on its first poll.
		status := statusOf(err)
		waiting := status == http.StatusForbidden || status == http.StatusNotFound
		if waiting && !errors.Is(err, ErrAuthExpired) && !errors.Is(err, ErrDeviceAuthDisabled) {
			return Token{}, ErrAuthPending
		}
		return Token{}, err
	}
	// The endpoint answers 200 with an empty body while the operator is still
	// in the browser, so absence of a code is the pending signal rather than a
	// status field.
	if strings.TrimSpace(out.AuthorizationCode) == "" {
		return Token{}, ErrAuthPending
	}

	return c.exchangeCode(ctx, out.AuthorizationCode, out.CodeVerifier)
}

// RefreshToken trades a refresh token for a fresh pair. The refresh token
// rotates, so the result must be persisted before the next call.
func (c *Client) RefreshToken(ctx context.Context, refresh string) (Token, error) {
	refresh = strings.TrimSpace(refresh)
	if refresh == "" {
		return Token{}, ErrNotSignedIn
	}
	var out tokenResponse
	if err := c.postJSON(ctx, c.endpoints.OAuthToken, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     ClientID,
		"refresh_token": refresh,
	}, &out); err != nil {
		return Token{}, err
	}
	return newToken(out, refresh)
}

// exchangeCode trades the authorization code the poll returned for a token
// pair. Form-encoded, unlike every other call here: the OAuth endpoint takes
// JSON for a refresh but the code grant only in the RFC 6749 spelling.
func (c *Client) exchangeCode(ctx context.Context, code, verifier string) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {c.endpoints.RedirectURI},
	}
	var out tokenResponse
	if err := c.postForm(ctx, c.endpoints.OAuthToken, form, &out); err != nil {
		return Token{}, err
	}
	return newToken(out, "")
}

// tokenResponse is the shape both grants answer with. Every field is optional:
// a refresh that rotates nothing sends back only an access token.
type tokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	IDToken      string  `json:"id_token"`
	ExpiresIn    flexInt `json:"expires_in"`
}

// newToken turns a token response into what we store. keepRefresh is the
// refresh token we sent, kept when the issuer rotates nothing -- dropping it
// would sign the account out at the next restart.
func newToken(out tokenResponse, keepRefresh string) (Token, error) {
	if strings.TrimSpace(out.AccessToken) == "" {
		return Token{}, fmt.Errorf("chatgpt: token response carried no access token")
	}
	tok := Token{
		Access:    out.AccessToken,
		Refresh:   strings.TrimSpace(out.RefreshToken),
		IDToken:   out.IDToken,
		ExpiresAt: expiryOf(out.AccessToken, time.Duration(out.ExpiresIn)*time.Second),
	}
	if tok.Refresh == "" {
		tok.Refresh = strings.TrimSpace(keepRefresh)
	}
	tok.AccountID, tok.Plan, tok.Email = identityFromIDToken(tok.IDToken)
	return tok, nil
}

// expiryOf reads the access token's own exp claim, and falls back to expires_in
// for issuers that send one. The refresh grant returns neither an expires_in
// nor anything else about lifetime, so the claim is the only honest answer
// there; an hour is the last resort, because a zero ExpiresAt reads as "never
// refresh" and would strand the account on a dead token.
func expiryOf(accessToken string, expiresIn time.Duration) time.Time {
	if exp, ok := jwtExpiry(accessToken); ok {
		return exp
	}
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}
	return time.Now().Add(expiresIn)
}

func (c *Client) postJSON(ctx context.Context, endpoint string, in any, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	return c.post(ctx, endpoint, "application/json", payload, out)
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	return c.post(ctx, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode()), out)
}

func (c *Client) post(ctx context.Context, endpoint, contentType string, payload []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	// The auth host is as originator-sensitive as the inference host.
	req.Header.Set("originator", codexOriginator)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("chatgpt auth request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read chatgpt auth response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return authError(resp.StatusCode, raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		// Not an error: the poll endpoint answers empty while it waits, and
		// the caller reads that as ErrAuthPending from the zero value.
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode chatgpt auth response: %w", err)
	}
	return nil
}

// authError turns an auth-host failure into something an admin can act on.
//
// The two that matter are told apart by the body rather than the status: OpenAI
// returns 400 for both "still waiting" and "your account may not do this", and
// showing the second as a generic 400 would leave the operator staring at a
// code that will never work.
func authError(status int, body []byte) error {
	return &httpError{status: status, err: authReason(status, body)}
}

// httpError carries the status alongside the reason, because the same status
// means different things at different legs of the flow: 403 is "still waiting"
// while polling and "your account may not do this" when asking for a code.
type httpError struct {
	status int
	err    error
}

func (e *httpError) Error() string { return e.err.Error() }
func (e *httpError) Unwrap() error { return e.err }

// statusOf is the HTTP status behind err, or 0 if it did not come from one.
func statusOf(err error) int {
	var he *httpError
	if errors.As(err, &he) {
		return he.status
	}
	return 0
}

func authReason(status int, body []byte) error {
	var parsed struct {
		Error       string `json:"error"`
		Detail      string `json:"detail"`
		Description string `json:"error_description"`
		Message     string `json:"message"`
	}
	_ = json.Unmarshal(body, &parsed)

	text := strings.ToLower(strings.Join([]string{
		parsed.Error, parsed.Detail, parsed.Description, parsed.Message, string(body),
	}, " "))

	switch {
	case strings.Contains(text, "authorization_pending"), strings.Contains(text, "slow_down"):
		return ErrAuthPending
	case strings.Contains(text, "expired_token"), strings.Contains(text, "expired"):
		return ErrAuthExpired
	case strings.Contains(text, "device") && (strings.Contains(text, "disabled") ||
		strings.Contains(text, "not allowed") || strings.Contains(text, "not enabled")):
		return ErrDeviceAuthDisabled
	}

	msg := strings.TrimSpace(firstNonEmpty(parsed.Description, parsed.Detail, parsed.Message, parsed.Error, string(body)))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return fmt.Errorf("chatgpt auth: HTTP %d: %s", status, msg)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
