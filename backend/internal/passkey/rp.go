package passkey

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// ErrUnsupportedOrigin means this request cannot carry a WebAuthn ceremony at
// all: the relying-party ID has to be a domain name. Callers turn it into a 4xx
// with the message below rather than a 500, because nothing is broken — the app
// is simply being reached by an address passkeys cannot be bound to.
var ErrUnsupportedOrigin = errors.New("passkeys require a hostname, not an IP address")

// ErrLoopbackHost is the loopback-literal case, split out because it has a
// one-step fix worth naming.
var ErrLoopbackHost = errors.New("passkeys need the hostname localhost, not a loopback IP address")

// ErrInsecureContext means the page is plain HTTP on something other than
// localhost. The browser refuses to run a ceremony outside a secure context, so
// catching it here turns an opaque NotAllowedError in the console into an answer.
var ErrInsecureContext = errors.New("passkeys require HTTPS outside localhost")

// IsOriginError reports whether err is one of the origin problems above, which
// callers render as a 4xx rather than a 500.
func IsOriginError(err error) bool {
	return errors.Is(err, ErrUnsupportedOrigin) ||
		errors.Is(err, ErrLoopbackHost) ||
		errors.Is(err, ErrInsecureContext)
}

// Message renders the user-facing explanation for an origin error.
func Message(err error) string {
	switch {
	case errors.Is(err, ErrLoopbackHost):
		return "Passkeys need the hostname localhost rather than a loopback IP address. Open this app at localhost (for example http://localhost:8090) and try again."
	case errors.Is(err, ErrInsecureContext):
		return "Passkeys need a secure connection. Serve this app over HTTPS, or open it from localhost."
	default:
		return "Passkeys need a hostname, not an IP address, and the page must be served over HTTPS (or from localhost). Reach this app by its domain name, or set PASSKEY_RP_ID and PASSKEY_ORIGINS."
	}
}

// Env overrides. Both are read straight from the environment on each use (like
// config.StagingMaxBytesFromEnv) rather than from app_settings, because they
// describe how the deployment is reached, not a preference somebody would edit in
// the Settings page. Getting them wrong breaks sign-in, and the operator who owns
// the reverse proxy is the one who knows them.
const (
	EnvRPID    = "PASSKEY_RP_ID"
	EnvOrigins = "PASSKEY_ORIGINS"
)

// DefaultDisplayName is used when the instance has no app name set.
const DefaultDisplayName = "Lemmary"

// RelyingParty is the resolved WebAuthn relying-party identity for one request.
type RelyingParty struct {
	ID          string
	DisplayName string
	Origins     []string
}

// Caller describes the request a ceremony is being resolved for.
type Caller struct {
	// Scheme the browser used, as far as it can be determined.
	Scheme string
	// Host header, with port.
	Host string
	// Origin header, when the browser sent one. Present on the cross-origin
	// requests the dev server makes, absent on same-origin form posts.
	Origin string
}

// ResolveRelyingParty works out the relying-party ID and the acceptable origins.
//
// The env overrides win outright when set. Otherwise both are derived from the
// request, which is what lets a self-hosted install work with no configuration at
// all: the RP ID is the request host with any port stripped, and the origin list
// carries both the http and https forms of host:port.
//
// Listing both schemes is deliberate. Behind a TLS-terminating reverse proxy the
// server sees plain HTTP and cannot tell what the browser used, and
// X-Forwarded-Proto is client-controlled so trusting it would only move the
// guess. Accepting either scheme costs nothing: the browser reports its true
// origin in clientDataJSON, so the only entry that can ever match is the origin
// the user actually loaded. The RP ID — which is what a credential is
// permanently bound to, and what makes it unusable on any other domain — carries
// no scheme at all, so it is unaffected either way.
func ResolveRelyingParty(caller Caller, displayName string) (RelyingParty, error) {
	rp := RelyingParty{DisplayName: strings.TrimSpace(displayName)}
	if rp.DisplayName == "" {
		rp.DisplayName = DefaultDisplayName
	}

	originsOverride := strings.TrimSpace(os.Getenv(EnvOrigins))

	if override := strings.TrimSpace(os.Getenv(EnvRPID)); override != "" {
		// The operator named the relying party, so none of the host-derived
		// checks below apply: they exist to work out what the browser sees, and
		// this is the operator telling us directly.
		rp.ID = override
	} else {
		id, err := rpIDFromHost(caller.Host)
		if err != nil {
			return RelyingParty{}, err
		}
		// WebAuthn only runs in a secure context. Checking it here turns what
		// would otherwise be a bare NotAllowedError in the browser console into an
		// answer. Skipped when the origins are configured, because that is an
		// operator asserting what the browser actually loaded — the usual case
		// being a TLS-terminating proxy that does not set X-Forwarded-Proto.
		if caller.Scheme != "https" && id != "localhost" && originsOverride == "" {
			return RelyingParty{}, ErrInsecureContext
		}
		rp.ID = id
	}

	if originsOverride != "" {
		for _, origin := range strings.Split(originsOverride, ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
				rp.Origins = append(rp.Origins, origin)
			}
		}
		return rp, nil
	}

	hostPort := strings.TrimSpace(caller.Host)
	if hostPort == "" {
		return RelyingParty{}, ErrUnsupportedOrigin
	}
	// The port stays on the origin — a browser's origin includes a non-default
	// one — and stays off the RP ID, which must be a bare domain.
	rp.Origins = []string{"https://" + hostPort, "http://" + hostPort}

	// The page and the API are not always the same origin. In development the SPA
	// is served by Vite on :5173 while the API answers on :8090, so the origin
	// derived from this request's own Host would never match what the browser
	// reports and every ceremony would fail verification.
	//
	// Accepting the Origin header closes that gap, but only when its host belongs
	// to the relying party already resolved above. That constraint is what keeps
	// it safe: the credential is bound to the RP ID, and the browser refuses to
	// run a ceremony whose RP ID is not a registrable suffix of the page's own
	// origin. So this can only ever admit an origin the browser would already
	// have been willing to use with this RP ID.
	if origin := sameRelyingPartyOrigin(caller.Origin, rp.ID); origin != "" {
		rp.Origins = append(rp.Origins, origin)
	}

	return rp, nil
}

// sameRelyingPartyOrigin returns the origin if it parses and its host is the
// relying party or a subdomain of it, and "" otherwise.
func sameRelyingPartyOrigin(origin, rpID string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" || rpID == "" {
		return ""
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	rpID = strings.ToLower(rpID)
	if host != rpID && !strings.HasSuffix(host, "."+rpID) {
		return ""
	}
	// Normalized rather than echoed, so a header with a path or trailing slash
	// cannot widen what is accepted.
	return parsed.Scheme + "://" + parsed.Host
}

// rpIDFromHost strips the port and rejects anything that cannot be a
// relying-party ID.
func rpIDFromHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", ErrUnsupportedOrigin
	}
	// SplitHostPort only succeeds when a port is present, and it is also what
	// unwraps the brackets around an IPv6 literal.
	if bare, _, err := net.SplitHostPort(host); err == nil {
		host = bare
	}
	host = strings.Trim(host, "[]")
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", ErrUnsupportedOrigin
	}
	// An IP address can never be a relying-party ID: the spec requires a
	// registrable domain, and "localhost" is the only non-domain browsers exempt.
	//
	// A loopback literal gets its own error rather than being quietly rewritten
	// to "localhost". Rewriting looks tempting — this app's default URL is
	// http://127.0.0.1:8090, which *is* a secure context — but the RP ID has to
	// be a registrable domain suffix of the origin the browser reports, and
	// "localhost" is not a suffix of "127.0.0.1". The ceremony would fail in the
	// browser with a bare SecurityError instead of telling anyone why. Naming the
	// fix is more useful than guessing at it.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return "", ErrLoopbackHost
		}
		return "", ErrUnsupportedOrigin
	}
	return strings.ToLower(host), nil
}

// New builds the go-webauthn instance for one request.
func New(caller Caller, displayName string) (*webauthn.WebAuthn, error) {
	rp, err := ResolveRelyingParty(caller, displayName)
	if err != nil {
		return nil, err
	}
	w, err := webauthn.New(&webauthn.Config{
		RPID:          rp.ID,
		RPDisplayName: rp.DisplayName,
		RPOrigins:     rp.Origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			// Required, not preferred, and this is a security property rather than
			// a preference.
			//
			// go-webauthn only checks the assertion's user-verified flag when the
			// session requirement is exactly "required"
			// (shouldVerifyUser in webauthn/login.go). Under "preferred" a
			// credential that verified nobody is accepted, so a PIN-less roaming
			// key would mint a full session on possession and a touch alone. A
			// passkey here replaces the password outright — it is the only factor —
			// and both the UI and docs promise a fingerprint, face or device PIN.
			//
			// The cost is that an authenticator with no PIN or biometric set cannot
			// be enrolled. That is the right trade for a sole factor, and the
			// remedy is in the owner's hands: set a PIN on the key.
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("configure webauthn for %q: %w", rp.ID, err)
	}
	return w, nil
}

// RequestScheme reports the scheme the browser used.
//
// The same shape as ngxapi.requestBaseURL (backend/internal/ngxapi/response.go),
// including its two guards: only the first hop's value, and only when it is a
// real scheme. The header is client-controlled, which is survivable here — the
// browser reports its true origin in clientDataJSON, so the worst a forged value
// can do is get a request past the secure-context gate and then fail origin
// verification. It is not reused from there because it is unexported in a
// different package, and promoting it for one more caller is more churn than the
// five lines are worth.
func RequestScheme(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ","); proto != "" {
		if proto = strings.TrimSpace(proto); proto == "http" || proto == "https" {
			scheme = proto
		}
	}
	return scheme
}

// CallerOf reads the relevant parts of an incoming request.
func CallerOf(r *http.Request) Caller {
	return Caller{
		Scheme: RequestScheme(r),
		Host:   r.Host,
		Origin: r.Header.Get("Origin"),
	}
}

// NewForRequest is the convenience form used by the handlers.
func NewForRequest(r *http.Request, displayName string) (*webauthn.WebAuthn, error) {
	return New(CallerOf(r), displayName)
}

// Available reports whether this request could carry a ceremony at all, so the
// public meta endpoint can tell the login screen whether to offer the button.
func Available(r *http.Request) bool {
	_, err := NewForRequest(r, DefaultDisplayName)
	return err == nil
}
