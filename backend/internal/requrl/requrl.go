// Package requrl reads the scheme and origin a client used to reach this
// server, which most installs run behind a TLS-terminating proxy.
package requrl

import (
	"net/http"
	"strings"
)

// Scheme reports the scheme the client used. Only the first X-Forwarded-Proto
// hop counts, and only when it is a real scheme: the header is client-controlled
// and multi-hop proxies join values with commas.
func Scheme(r *http.Request) string {
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

// Base is the scheme and host the client used, without a trailing slash.
func Base(r *http.Request) string {
	return Scheme(r) + "://" + r.Host
}
