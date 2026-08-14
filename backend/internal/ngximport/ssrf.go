package ngximport

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

var allowPrivateImportHosts atomic.Bool

// SetAllowPrivateImportHosts lets tests reach httptest servers on loopback.
// Production should leave this false and use IMPORT_ALLOW_PRIVATE when a
// self-hosted Paperless-ngx instance is on a private network.
func SetAllowPrivateImportHosts(allow bool) {
	allowPrivateImportHosts.Store(allow)
}

func privateImportHostsAllowed() bool {
	if allowPrivateImportHosts.Load() {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("IMPORT_ALLOW_PRIVATE"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func newImportHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   denyBlockedImportDial,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func denyBlockedImportDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("import blocked: invalid dial address")
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("import blocked: invalid dial address")
	}
	if blockedImportIP(ip) {
		return fmt.Errorf("import blocked: address %s is not allowed", ip)
	}
	return nil
}

func rejectBlockedImportURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return fmt.Errorf("url must include a host")
	}
	if ip := net.ParseIP(host); ip != nil && blockedImportIP(ip) {
		return fmt.Errorf("url host %s is not allowed", host)
	}
	return nil
}

func blockedImportIP(ip net.IP) bool {
	return blockedImportIPWithPrivate(ip, privateImportHostsAllowed())
}

func blockedImportIPWithPrivate(ip net.IP, allowPrivate bool) bool {
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if allowPrivate {
		return false
	}
	if cgnatIP(ip) {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func cgnatIP(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}
