package requrl

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSchemeReadsFirstForwardedHopOnly(t *testing.T) {
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
			if got := Scheme(r); got != tc.want {
				t.Fatalf("Scheme() = %q, want %q", got, tc.want)
			}
		})
	}
}
