package remoteimport

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/i18n"
)

func TestBlockedImportIP(t *testing.T) {
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
		if blockedImportIPWithPrivate(ip, false) == nil {
			t.Fatalf("expected %s to be blocked", raw)
		}
	}

	public := net.ParseIP("8.8.8.8")
	if err := blockedImportIPWithPrivate(public, false); err != nil {
		t.Fatalf("expected public IP to be allowed, got %v", err)
	}
}

func TestPrivateImportHostsAllowedByDefault(t *testing.T) {
	for env, want := range map[string]bool{
		"": true, "1": true, "on": true, "0": false, "false": false, "no": false, "off": false, "flase": false,
	} {
		t.Setenv("IMPORT_ALLOW_PRIVATE", env)
		if got := privateImportHostsAllowed(); got != want {
			t.Errorf("IMPORT_ALLOW_PRIVATE=%q: allowed=%v, want %v", env, got, want)
		}
	}
}

func TestManagedBlocksPrivateImportHosts(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "1")
	aiprovider.SetManaged(true)
	t.Cleanup(func() { aiprovider.SetManaged(false) })

	if privateImportHostsAllowed() {
		t.Fatal("a managed deployment must not reach private hosts, whatever the environment says")
	}
}

func TestPerDocumentRefusalReadsPlainly(t *testing.T) {
	t.Parallel()

	var r Result
	r.count("document 7 (x)", fmt.Errorf("download: %w", blockedImportIPWithPrivate(net.ParseIP("10.0.0.1"), false)))
	want := "document 7 (x): The import address 10.0.0.1 is on a private network, and this server refuses those (IMPORT_ALLOW_PRIVATE=0)."
	if len(r.Errors) != 1 || r.Errors[0] != want {
		t.Fatalf("errors = %q, want %q", r.Errors, want)
	}
}

func TestPrivateImportHostsStillBlockLinkLocal(t *testing.T) {
	t.Parallel()

	if blockedImportIPWithPrivate(net.ParseIP("127.0.0.1"), true) != nil {
		t.Fatal("loopback should be allowed when private hosts are enabled")
	}
	if blockedImportIPWithPrivate(net.ParseIP("192.168.0.5"), true) != nil {
		t.Fatal("RFC1918 should be allowed when private hosts are enabled")
	}
	for _, raw := range []string{"169.254.169.254", "100.100.100.200", "fd00:ec2::254"} {
		if blockedImportIPWithPrivate(net.ParseIP(raw), true) == nil {
			t.Fatalf("metadata endpoint %s should stay blocked", raw)
		}
	}
	if blockedImportIPWithPrivate(net.ParseIP("100.64.0.1"), true) != nil {
		t.Fatal("CGNAT should be allowed when private hosts are enabled")
	}
}

func TestSafeClientBlocksLoopback(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "0")
	SetAllowPrivateImportHosts(false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("loopback request should not be dialed")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	want := "The import address 127.0.0.1 is on a private network, and this server refuses those (IMPORT_ALLOW_PRIVATE=0)."
	if _, err := NewClient(srv.URL, "k", nil); err == nil || err.Error() != want {
		t.Fatalf("literal loopback URL: got %v", err)
	}

	client, err := NewClient(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListTags()
	if got := userFacing(err); got == nil || !strings.HasPrefix(got.Error(), "The import address ") || !strings.Contains(got.Error(), "private network") {
		t.Fatalf("expected the refusal alone, got %v", got)
	}
	if got := i18n.Of("de", err); !strings.HasPrefix(got, "Die Importadresse ") {
		t.Fatalf("expected a German refusal, got %q", got)
	}
}

func TestSafeClientBlocksLinkLocalLiteral(t *testing.T) {
	SetAllowPrivateImportHosts(true)
	t.Cleanup(func() { SetAllowPrivateImportHosts(false) })

	_, err := NewClient("http://169.254.169.254/", "k", nil)
	if err == nil || !strings.Contains(err.Error(), "never allowed") {
		t.Fatalf("expected link-local URL to be rejected, got %v", err)
	}
}

func TestSafeClientBlocksRedirectToLinkLocal(t *testing.T) {
	SetAllowPrivateImportHosts(true)
	t.Cleanup(func() { SetAllowPrivateImportHosts(false) })

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewClient(srv.URL, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListTags()
	if err == nil || !strings.Contains(err.Error(), "never allowed") {
		t.Fatalf("expected redirect to link-local to be blocked, got %v", err)
	}
}

func TestSafeClientAllowsPrivateWhenEnabled(t *testing.T) {
	SetAllowPrivateImportHosts(true)
	t.Cleanup(func() { SetAllowPrivateImportHosts(false) })

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count":    0,
			"next":     nil,
			"previous": nil,
			"results":  []namedEntity{},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewClient(srv.URL, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	tags, err := client.ListTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Fatalf("tags=%v", tags)
	}
}

func TestClientHidesRemoteErrorBody(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "SECRET_INTERNAL_BODY")
	})
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/download") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "SECRET_DOWNLOAD_BODY")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewClient(srv.URL, "k", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListTags()
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("expected status without body, got %v", err)
	}
	_, err = client.DownloadDocument(1)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("expected download status without body, got %v", err)
	}
}
