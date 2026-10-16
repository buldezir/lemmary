package remoteimport

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"lemmary/backend/internal/i18n"
)

var allowPrivateImportHosts atomic.Bool

// SetAllowPrivateImportHosts lets tests reach httptest servers on loopback
// even under IMPORT_ALLOW_PRIVATE=0.
func SetAllowPrivateImportHosts(allow bool) {
	allowPrivateImportHosts.Store(allow)
}

func privateImportHostsAllowed() bool {
	if allowPrivateImportHosts.Load() {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("IMPORT_ALLOW_PRIVATE"))) {
	case "0", "false", "no":
		return false
	default:
		return true
	}
}

// userFacing drops the request and dial context around a refusal, which is all
// the user needs to read.
func userFacing(err error) error {
	if e, ok := errors.AsType[*i18n.Error](err); ok {
		return e
	}
	return err
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
	return blockedImportIP(ip)
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
	if ip := net.ParseIP(host); ip != nil {
		return blockedImportIP(ip)
	}
	return nil
}

func blockedImportIP(ip net.IP) error {
	return blockedImportIPWithPrivate(ip, privateImportHostsAllowed())
}

func blockedImportIPWithPrivate(ip net.IP, allowPrivate bool) error {
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return i18n.Errorf("The import address %s is link-local, multicast or a cloud metadata endpoint, which is never allowed.", ip)
	}
	if !allowPrivate && (cgnatIP(ip) || ip.IsLoopback() || ip.IsPrivate()) {
		return i18n.Errorf("The import address %s is on a private network, and this server refuses those (IMPORT_ALLOW_PRIVATE=0).", ip)
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
