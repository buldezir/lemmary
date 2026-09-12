package escl

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// allowLoopback lets tests reach an httptest server. There is no environment
// switch for it: a scanner is never on loopback, and an operator who could turn
// this on could turn the endpoint into a probe of the host's own services.
var allowLoopback atomic.Bool

// SetAllowLoopback is for tests only.
func SetAllowLoopback(allow bool) { allowLoopback.Store(allow) }

// newClient builds the HTTP client every eSCL call goes through.
//
// The address comes from whoever is signed in, so this is a request-forgery
// boundary: without the dial guard, "scan from 169.254.169.254" would make the
// server fetch cloud metadata, and a sweep of 0.0.0.0/0 would make it a port
// scanner. internal/ngximport has the same shape for the Paperless import, but
// the opposite polarity -- that one blocks private addresses, and a scanner
// lives nowhere else. So this is an allowlist of exactly the private space:
// RFC1918 and IPv6 ULA, and nothing else, link-local included.
//
// No client-wide Timeout: one scan is a POST, one GET per page and a DELETE,
// each with its own deadline, and a feeder run is minutes of legitimate work.
func newClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   denyPublicDial,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	return &http.Client{
		Transport: transport,
		// A scanner redirecting elsewhere has nothing legitimate to say, and
		// following it would step around the URL check.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func denyPublicDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("scan blocked: invalid dial address")
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if !allowedScanIP(ip) {
		return fmt.Errorf("scan blocked: %s is not a private network address", host)
	}
	return nil
}

// allowedScanIP reports whether an address may be scanned from.
//
// Link-local is refused along with everything public: 169.254.169.254 is the
// cloud metadata service on every major host, and a scanner that fell back to
// an APIPA address has no working network anyway.
func allowedScanIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if allowLoopback.Load() && ip.IsLoopback() {
		return true
	}
	return ip.IsPrivate()
}

// normalizeBase turns whatever the user typed into the base URL of a scanner's
// eSCL resource: "192.168.1.9" and "http://192.168.1.9/" both become
// "http://192.168.1.9/eSCL". A path that is already there is kept, which is how
// a device that advertises a different resource root over mDNS still works.
func normalizeBase(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("scanner address is required")
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid scanner address")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scanner address must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("scanner address must include a host")
	}
	// A name would be resolved at dial time, where the guard still catches it,
	// but the error then reads like a network failure. Refusing here says why.
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("scanner address must be an IP address on your local network")
	}
	if !allowedScanIP(ip) {
		return "", fmt.Errorf("%s is not a private network address", host)
	}

	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/" + defaultResource
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
