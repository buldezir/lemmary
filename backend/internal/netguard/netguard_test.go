package netguard

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/i18n"
)

func TestBlockedIP(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"127.0.0.1",
		"::1",
		"10.0.0.1",
		"192.168.1.10",
		"172.16.0.1",
		"169.254.169.254",
		"0.0.0.0",
		"224.0.0.1",
		"100.64.0.1",
		"fe80::1",
	}
	for _, raw := range blocked {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("parse %q", raw)
		}
		if blockedIPWithPrivate(ip, false) == nil {
			t.Fatalf("expected %s to be blocked", raw)
		}
	}

	public := net.ParseIP("8.8.8.8")
	if err := blockedIPWithPrivate(public, false); err != nil {
		t.Fatalf("expected public IP to be allowed, got %v", err)
	}
}

func TestPrivateHostsAllowedByDefault(t *testing.T) {
	for env, want := range map[string]bool{
		"": true, "1": true, "on": true, "0": false, "false": false, "no": false, "off": false, "flase": false,
	} {
		t.Setenv("IMPORT_ALLOW_PRIVATE", env)
		if got := privateHostsAllowed(); got != want {
			t.Errorf("IMPORT_ALLOW_PRIVATE=%q: allowed=%v, want %v", env, got, want)
		}
	}
}

func TestManagedBlocksPrivateHosts(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "1")
	aiprovider.SetManaged(true)
	t.Cleanup(func() { aiprovider.SetManaged(false) })

	if privateHostsAllowed() {
		t.Fatal("a managed deployment must not reach private hosts, whatever the environment says")
	}
}

func TestPrivateHostsStillBlockLinkLocal(t *testing.T) {
	t.Parallel()

	if blockedIPWithPrivate(net.ParseIP("127.0.0.1"), true) != nil {
		t.Fatal("loopback should be allowed when private hosts are enabled")
	}
	if blockedIPWithPrivate(net.ParseIP("192.168.0.5"), true) != nil {
		t.Fatal("RFC1918 should be allowed when private hosts are enabled")
	}
	for _, raw := range []string{"169.254.169.254", "100.100.100.200", "fd00:ec2::254"} {
		if blockedIPWithPrivate(net.ParseIP(raw), true) == nil {
			t.Fatalf("metadata endpoint %s should stay blocked", raw)
		}
	}
	if blockedIPWithPrivate(net.ParseIP("100.64.0.1"), true) != nil {
		t.Fatal("CGNAT should be allowed when private hosts are enabled")
	}
}

func TestCheckURL(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "0")

	want := "The address 10.0.0.1 is on a private network, and this server refuses those (IMPORT_ALLOW_PRIVATE=0)."
	if err := CheckURL("http://10.0.0.1/hook"); err == nil || err.Error() != want {
		t.Fatalf("private literal: got %v", err)
	}
	if got := i18n.Of("de", CheckURL("http://10.0.0.1/hook")); !strings.HasPrefix(got, "Die Adresse 10.0.0.1 ") {
		t.Fatalf("expected a German refusal, got %q", got)
	}
	if err := CheckURL("http://169.254.169.254/"); err == nil || !strings.Contains(err.Error(), "never allowed") {
		t.Fatalf("link-local literal: got %v", err)
	}
	if err := CheckURL("http:///path"); err == nil {
		t.Fatal("a URL without a host must be refused")
	}
	if err := CheckURL("https://hooks.example.com/x"); err != nil {
		t.Fatalf("a hostname is checked at dial time, not here: %v", err)
	}
}

func TestClientRefusesHostnameResolvingToLoopback(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "0")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("loopback request should not be dialed")
	}))
	t.Cleanup(srv.Close)

	resp, err := Client(5 * time.Second).Get(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected the dial to be refused")
	}
	if !strings.Contains(err.Error(), "private network") {
		t.Fatalf("expected a private-network refusal, got %v", err)
	}
}
