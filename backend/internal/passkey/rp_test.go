package passkey

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRPIDFromHost(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		want    string
		wantErr error
	}{
		{name: "bare hostname", host: "archive.example.com", want: "archive.example.com"},
		{name: "hostname with port", host: "archive.example.com:8090", want: "archive.example.com"},
		{name: "uppercase is normalized", host: "Archive.Example.COM", want: "archive.example.com"},
		{name: "trailing dot is trimmed", host: "archive.example.com.", want: "archive.example.com"},
		{name: "localhost", host: "localhost", want: "localhost"},
		{name: "localhost with port", host: "localhost:8090", want: "localhost"},
		// The app's own default URL. It is a secure context, so the browser would
		// run the ceremony, but no valid RP ID can be derived from it.
		{name: "loopback ipv4", host: "127.0.0.1:8090", wantErr: ErrLoopbackHost},
		{name: "loopback ipv6", host: "[::1]:8090", wantErr: ErrLoopbackHost},
		{name: "lan ipv4", host: "192.168.1.10:8090", wantErr: ErrUnsupportedOrigin},
		{name: "public ipv4", host: "203.0.113.7", wantErr: ErrUnsupportedOrigin},
		{name: "ipv6 literal", host: "[2001:db8::1]:8090", wantErr: ErrUnsupportedOrigin},
		{name: "empty", host: "", wantErr: ErrUnsupportedOrigin},
		{name: "whitespace only", host: "   ", wantErr: ErrUnsupportedOrigin},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := rpIDFromHost(tc.host)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("rpIDFromHost(%q) error = %v, want %v", tc.host, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("rpIDFromHost(%q) unexpected error: %v", tc.host, err)
			}
			if got != tc.want {
				t.Fatalf("rpIDFromHost(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

func TestResolveRelyingPartyDerivesFromHost(t *testing.T) {
	rp, err := ResolveRelyingParty(Caller{Scheme: "https", Host: "archive.example.com:8443"}, "My Archive")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.ID != "archive.example.com" {
		t.Fatalf("ID = %q, want archive.example.com", rp.ID)
	}
	if rp.DisplayName != "My Archive" {
		t.Fatalf("DisplayName = %q, want My Archive", rp.DisplayName)
	}
	// Both schemes, because a TLS-terminating proxy makes the server's own view
	// of the scheme unreliable and the browser reports the true origin anyway.
	want := []string{"https://archive.example.com:8443", "http://archive.example.com:8443"}
	if len(rp.Origins) != len(want) {
		t.Fatalf("Origins = %v, want %v", rp.Origins, want)
	}
	for i := range want {
		if rp.Origins[i] != want[i] {
			t.Fatalf("Origins = %v, want %v", rp.Origins, want)
		}
	}
}

func TestResolveRelyingPartyFallsBackToDefaultDisplayName(t *testing.T) {
	rp, err := ResolveRelyingParty(Caller{Scheme: "https", Host: "archive.example.com"}, "   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.DisplayName != DefaultDisplayName {
		t.Fatalf("DisplayName = %q, want %q", rp.DisplayName, DefaultDisplayName)
	}
}

func TestResolveRelyingPartyEnvOverrides(t *testing.T) {
	// A LAN IP is exactly the case the overrides exist for: the request host
	// cannot yield an RP ID, but the operator knows the public hostname.
	t.Setenv(EnvRPID, "archive.example.com")
	t.Setenv(EnvOrigins, " https://archive.example.com , https://www.archive.example.com ")

	rp, err := ResolveRelyingParty(Caller{Scheme: "https", Host: "192.168.1.10:8090"}, "Lemmary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.ID != "archive.example.com" {
		t.Fatalf("ID = %q, want the override", rp.ID)
	}
	want := []string{"https://archive.example.com", "https://www.archive.example.com"}
	if len(rp.Origins) != len(want) {
		t.Fatalf("Origins = %v, want %v", rp.Origins, want)
	}
	for i := range want {
		if rp.Origins[i] != want[i] {
			t.Fatalf("Origins = %v, want %v", rp.Origins, want)
		}
	}
}

func TestResolveRelyingPartyOriginOverrideAloneStillNeedsAUsableHost(t *testing.T) {
	// Setting only the origins does not rescue an IP-address host: the RP ID is
	// the part that cannot be derived, so the request must still be refused
	// rather than silently binding credentials to something unexpected.
	t.Setenv(EnvOrigins, "https://archive.example.com")

	if _, err := ResolveRelyingParty(Caller{Scheme: "https", Host: "192.168.1.10:8090"}, "Lemmary"); !IsOriginError(err) {
		t.Fatalf("error = %v, want an origin error", err)
	}
}

func TestNewRejectsUnusableHost(t *testing.T) {
	if _, err := New(Caller{Scheme: "https", Host: "192.168.1.10:8090"}, "Lemmary"); !IsOriginError(err) {
		t.Fatalf("error = %v, want an origin error", err)
	}
}

func TestNewBuildsInstanceForHostname(t *testing.T) {
	w, err := New(Caller{Scheme: "https", Host: "archive.example.com"}, "Lemmary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Config.RPID != "archive.example.com" {
		t.Fatalf("RPID = %q, want archive.example.com", w.Config.RPID)
	}
}

func TestMessageDistinguishesEachOriginProblem(t *testing.T) {
	t.Parallel()
	// Each of the three has a different fix, so each needs its own message.
	messages := map[string]bool{
		Message(ErrLoopbackHost):      true,
		Message(ErrInsecureContext):   true,
		Message(ErrUnsupportedOrigin): true,
	}
	if len(messages) != 3 {
		t.Fatal("each origin error should get its own message; their fixes differ")
	}
	for _, err := range []error{ErrLoopbackHost, ErrInsecureContext, ErrUnsupportedOrigin} {
		if !IsOriginError(err) {
			t.Fatalf("%v must be recognized as an origin error", err)
		}
	}
}

func TestPlainHTTPOnARealHostnameIsRejected(t *testing.T) {
	// http://lemmary.lan is not a secure context, so the browser would refuse the
	// ceremony. Catching it here is the difference between an explanation and an
	// unexplained NotAllowedError in the console.
	if _, err := ResolveRelyingParty(Caller{Scheme: "http", Host: "lemmary.lan:8090"}, "Lemmary"); err != ErrInsecureContext {
		t.Fatalf("error = %v, want ErrInsecureContext", err)
	}
}

func TestPlainHTTPOnLocalhostIsAllowed(t *testing.T) {
	// The one exemption browsers make, and how this app is developed.
	rp, err := ResolveRelyingParty(Caller{Scheme: "http", Host: "localhost:8090"}, "Lemmary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.ID != "localhost" {
		t.Fatalf("ID = %q, want localhost", rp.ID)
	}
}

func TestConfiguredOriginsSuppressTheSecureContextGate(t *testing.T) {
	// The reverse-proxy case: the server sees plain HTTP, the browser saw HTTPS,
	// and the operator has said so by configuring the origins.
	t.Setenv(EnvOrigins, "https://archive.example.com")

	rp, err := ResolveRelyingParty(Caller{Scheme: "http", Host: "archive.example.com"}, "Lemmary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.ID != "archive.example.com" {
		t.Fatalf("ID = %q, want archive.example.com", rp.ID)
	}
}

func TestSameRelyingPartyOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		rpID   string
		want   string
	}{
		// The development setup: Vite serves the page on :5173 while the API
		// answers on :8090, so the browser's origin is not the one this request's
		// own Host would produce.
		{name: "different port, same host", origin: "http://localhost:5173", rpID: "localhost", want: "http://localhost:5173"},
		{name: "subdomain of the relying party", origin: "https://app.example.com", rpID: "example.com", want: "https://app.example.com"},
		{name: "exact match", origin: "https://example.com", rpID: "example.com", want: "https://example.com"},
		{name: "case is normalized", origin: "https://APP.Example.COM", rpID: "example.com", want: "https://APP.Example.COM"},
		// The constraint that makes trusting the header safe at all.
		{name: "unrelated host is refused", origin: "https://evil.com", rpID: "example.com", want: ""},
		{name: "suffix without a dot boundary is refused", origin: "https://notexample.com", rpID: "example.com", want: ""},
		{name: "non-http scheme is refused", origin: "file://example.com", rpID: "example.com", want: ""},
		{name: "the null origin is refused", origin: "null", rpID: "example.com", want: ""},
		{name: "empty is refused", origin: "", rpID: "example.com", want: ""},
		// A path or trailing slash must not widen what is accepted.
		{name: "path is stripped", origin: "https://example.com/evil", rpID: "example.com", want: "https://example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sameRelyingPartyOrigin(tc.origin, tc.rpID); got != tc.want {
				t.Fatalf("sameRelyingPartyOrigin(%q, %q) = %q, want %q", tc.origin, tc.rpID, got, tc.want)
			}
		})
	}
}

func TestDevSplitOriginIsAccepted(t *testing.T) {
	// Without this the whole dev workflow would fail origin verification: the SPA
	// on :5173 talking to the API on :8090 never matches an origin derived from
	// the API's own host.
	rp, err := ResolveRelyingParty(Caller{
		Scheme: "http",
		Host:   "localhost:8090",
		Origin: "http://localhost:5173",
	}, "Lemmary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rp.ID != "localhost" {
		t.Fatalf("ID = %q, want localhost", rp.ID)
	}
	found := false
	for _, origin := range rp.Origins {
		if origin == "http://localhost:5173" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Origins = %v, want the browser's own origin among them", rp.Origins)
	}
}

func TestForeignOriginHeaderIsNotAccepted(t *testing.T) {
	rp, err := ResolveRelyingParty(Caller{
		Scheme: "https",
		Host:   "archive.example.com",
		Origin: "https://evil.example.net",
	}, "Lemmary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, origin := range rp.Origins {
		if origin == "https://evil.example.net" {
			t.Fatalf("Origins = %v, must not include an unrelated origin", rp.Origins)
		}
	}
}

func TestConfiguredOriginsAreNotWidenedByTheHeader(t *testing.T) {
	// An operator who listed the origins gets exactly that list; the header is
	// not allowed to add to it.
	t.Setenv(EnvOrigins, "https://archive.example.com")

	rp, err := ResolveRelyingParty(Caller{
		Scheme: "https",
		Host:   "archive.example.com",
		Origin: "https://other.archive.example.com",
	}, "Lemmary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rp.Origins) != 1 || rp.Origins[0] != "https://archive.example.com" {
		t.Fatalf("Origins = %v, want only the configured one", rp.Origins)
	}
}

func TestRequestSchemeReadsFirstForwardedHopOnly(t *testing.T) {
	cases := []struct {
		name      string
		tls       bool
		forwarded string
		want      string
	}{
		{name: "plain", want: "http"},
		{name: "tls", tls: true, want: "https"},
		{name: "forwarded https over a plain listener", forwarded: "https", want: "https"},
		{name: "first hop wins", forwarded: "https, http", want: "https"},
		{name: "padded value is trimmed", forwarded: "  https  ", want: "https"},
		// The header is client-controlled, so a value that is not a scheme is
		// ignored rather than propagated.
		{name: "garbage is ignored", forwarded: "javascript:", want: "http"},
		{name: "empty is ignored", forwarded: "", want: "http"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "/api/app/passkeys/login/begin", nil)
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := RequestScheme(r); got != tc.want {
				t.Fatalf("RequestScheme() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAvailableTracksWhetherACeremonyCouldRun(t *testing.T) {
	t.Parallel()
	secure := httptest.NewRequest(http.MethodGet, "/api/app/meta", nil)
	secure.Host = "archive.example.com"
	secure.Header.Set("X-Forwarded-Proto", "https")
	if !Available(secure) {
		t.Fatal("an https hostname should be usable")
	}

	byIP := httptest.NewRequest(http.MethodGet, "/api/app/meta", nil)
	byIP.Host = "192.168.1.10:8090"
	if Available(byIP) {
		t.Fatal("an IP address can never carry a passkey ceremony")
	}
}
