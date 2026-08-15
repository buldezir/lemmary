package ngximport

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		if !blockedImportIPWithPrivate(ip, false) {
			t.Fatalf("expected %s to be blocked", raw)
		}
	}

	public := net.ParseIP("8.8.8.8")
	if blockedImportIPWithPrivate(public, false) {
		t.Fatal("expected public IP to be allowed")
	}
}

func TestPrivateImportHostsStillBlockLinkLocal(t *testing.T) {
	t.Parallel()

	if blockedImportIPWithPrivate(net.ParseIP("127.0.0.1"), true) {
		t.Fatal("loopback should be allowed when private hosts are enabled")
	}
	if blockedImportIPWithPrivate(net.ParseIP("192.168.0.5"), true) {
		t.Fatal("RFC1918 should be allowed when private hosts are enabled")
	}
	if !blockedImportIPWithPrivate(net.ParseIP("169.254.169.254"), true) {
		t.Fatal("link-local metadata should stay blocked")
	}
	if blockedImportIPWithPrivate(net.ParseIP("100.64.0.1"), true) {
		t.Fatal("CGNAT should be allowed when private hosts are enabled")
	}
}

func TestSafeClientBlocksLoopback(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "")
	SetAllowPrivateImportHosts(false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("loopback request should not be dialed")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(srv.URL, "k", nil)
	if err != nil {
		if strings.Contains(err.Error(), "not allowed") {
			return
		}
		t.Fatal(err)
	}
	_, err = client.ListTags()
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected blocked loopback, got %v", err)
	}
}

func TestSafeClientBlocksLinkLocalLiteral(t *testing.T) {
	SetAllowPrivateImportHosts(true)
	t.Cleanup(func() { SetAllowPrivateImportHosts(false) })

	_, err := NewClient("http://169.254.169.254/", "k", nil)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
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
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
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
