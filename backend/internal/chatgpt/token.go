// Package chatgpt signs Lemmary in to OpenAI's Codex backend with a ChatGPT
// subscription, and makes that backend answer the Chat Completions requests the
// rest of the codebase already sends.
//
// Three pieces, in the order they run:
//
//   - deviceauth.go mints a token pair from a code the operator types into a
//     browser. There is no redirect listener, so it works on a headless server.
//   - source.go keeps that pair fresh and writes rotations back to the
//     ai_providers row.
//   - transport.go rewrites POST /chat/completions into POST /responses on the
//     way out and the SSE answer back into a chat completion on the way in, as
//     an openai-go middleware. Nothing above it -- not ai.CompleteChat, not the
//     extractor, chatter, splitter, helper or search agent -- knows any of this
//     happened.
//
// Every endpoint here is OpenAI's own, undocumented, and meant for OpenAI's
// clients. That is why the feature is off unless AI_CHATGPT_LOGIN=1 and refused
// outright on a managed instance: pointing an account at them is a decision the
// operator makes for themselves. See docs/chatgpt_login.md.
package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Token is one signed-in ChatGPT account, as stored in ai_providers.oauth.
//
// Refresh is the durable half: access tokens last about an hour, and the
// refresh token is what survives a restart. It rotates on every use, so a
// refresh that succeeds must be persisted or the next one fails.
type Token struct {
	Access    string    `json:"access_token"`
	Refresh   string    `json:"refresh_token"`
	IDToken   string    `json:"id_token,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`

	// Identity read out of IDToken at sign-in, so Settings can name the account
	// without decoding a JWT on every render and without the token itself ever
	// reaching the browser.
	AccountID string `json:"account_id,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Email     string `json:"email,omitempty"`
}

func (t Token) Valid() bool { return strings.TrimSpace(t.Access) != "" }

// Expired reports whether the access token is spent, counting leeway as spent.
// The leeway is what keeps a request that starts just under the wire from
// arriving just over it.
func (t Token) Expired(leeway time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(leeway).After(t.ExpiresAt)
}

// Marshal serializes for the oauth column. Encoding here rather than at the
// call sites keeps the column's shape the token's own business.
func (t Token) Marshal() (string, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("encode chatgpt token: %w", err)
	}
	return string(b), nil
}

// ParseToken reads the oauth column. An empty column is not an error: it is a
// provider row nobody has signed in to yet.
func ParseToken(raw string) (Token, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Token{}, nil
	}
	var t Token
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return Token{}, fmt.Errorf("decode chatgpt token: %w", err)
	}
	return t, nil
}

// identityFromIDToken pulls the account id, plan and email out of the id_token.
//
// The signature is not checked, deliberately: we just received this token over
// TLS from the issuer in answer to our own request, so there is no second party
// whose claims we would be trusting. Verifying it would mean fetching and
// pinning a JWKS to learn something we already know.
func identityFromIDToken(idToken string) (accountID, plan, email string) {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) < 2 {
		return "", "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", ""
	}
	var claims struct {
		Email string `json:"email"`
		Auth  struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
			ChatGPTPlanType  string `json:"chatgpt_plan_type"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", ""
	}
	return claims.Auth.ChatGPTAccountID, claims.Auth.ChatGPTPlanType, claims.Email
}

// jwtExpiry reads the exp claim out of a token. Unsigned and unverified, for
// the same reason identityFromIDToken does not verify: this is our own token,
// and the claim only decides when we refresh it.
//
// The refresh grant answers with no expires_in at all, so without this every
// refreshed token would carry a guessed hour -- fine when the real lifetime is
// an hour, a stack of 401s when it is shorter.
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}
