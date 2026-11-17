// Package netguard is the outbound HTTP client for addresses a user typed: a
// remote import's server, a rule's webhook. Link-local, multicast and cloud
// metadata addresses are always refused, after redirects too, since the check
// runs at dial time; private networks are refused under IMPORT_ALLOW_PRIVATE=0
// and on a managed instance.
package netguard

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/i18n"
)

var allowPrivateHosts atomic.Bool

// Cloud metadata endpoints outside link-local: Alibaba's sits in CGNAT and
// EC2's IPv6 one in ULA space, both of which private hosts would allow.
var metadataIPs = []net.IP{net.ParseIP("100.100.100.200"), net.ParseIP("fd00:ec2::254")}

// SetAllowPrivateHosts lets tests reach httptest servers on loopback even
// under IMPORT_ALLOW_PRIVATE=0.
func SetAllowPrivateHosts(allow bool) {
	allowPrivateHosts.Store(allow)
}

func privateHostsAllowed() bool {
	if allowPrivateHosts.Load() {
		return true
	}
	if aiprovider.Managed() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("IMPORT_ALLOW_PRIVATE"))) {
	case "", "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Client refuses blocked addresses when it dials, so a hostname that resolves
// to one, or a redirect to one, fails the same as a literal. It ignores
// HTTP_PROXY and HTTPS_PROXY.
func Client(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   denyBlockedDial,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	// Through a proxy the dial check would see the proxy, never the target.
	transport.Proxy = nil
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func denyBlockedDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("outbound request blocked: invalid dial address")
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("outbound request blocked: invalid dial address")
	}
	return blockedIP(ip)
}

// CheckURL refuses a URL whose host is a blocked literal address, so whoever
// typed it hears so before any request is made. A hostname passes here and is
// checked when it is dialed.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return fmt.Errorf("url must include a host")
	}
	if ip := net.ParseIP(host); ip != nil {
		return blockedIP(ip)
	}
	return nil
}

func blockedIP(ip net.IP) error {
	return blockedIPWithPrivate(ip, privateHostsAllowed())
}

func blockedIPWithPrivate(ip net.IP, allowPrivate bool) error {
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || slices.ContainsFunc(metadataIPs, ip.Equal) {
		return i18n.Errorf("The address %s is link-local, multicast or a cloud metadata endpoint, which is never allowed.", ip)
	}
	if !allowPrivate && (cgnatIP(ip) || ip.IsLoopback() || ip.IsPrivate()) {
		return i18n.Errorf("The address %s is on a private network, and this server refuses those (IMPORT_ALLOW_PRIVATE=0).", ip)
	}
	return nil
}

func cgnatIP(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}
