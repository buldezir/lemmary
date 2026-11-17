package remoteimport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/netguard"
)

func TestPerDocumentRefusalReadsPlainly(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "0")

	var r Result
	r.count("document 7 (x)", fmt.Errorf("download: %w", netguard.CheckURL("http://10.0.0.1/")))
	want := "document 7 (x): The address 10.0.0.1 is on a private network, and this server refuses those (IMPORT_ALLOW_PRIVATE=0)."
	if len(r.Errors) != 1 || r.Errors[0] != want {
		t.Fatalf("errors = %q, want %q", r.Errors, want)
	}
}

func TestSafeClientBlocksLoopback(t *testing.T) {
	t.Setenv("IMPORT_ALLOW_PRIVATE", "0")
	netguard.SetAllowPrivateHosts(false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("loopback request should not be dialed")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	want := "The address 127.0.0.1 is on a private network, and this server refuses those (IMPORT_ALLOW_PRIVATE=0)."
	if _, err := NewClient(srv.URL, "k", nil); err == nil || err.Error() != want {
		t.Fatalf("literal loopback URL: got %v", err)
	}

	client, err := NewClient(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListTags()
	if got := userFacing(err); got == nil || !strings.HasPrefix(got.Error(), "The address ") || !strings.Contains(got.Error(), "private network") {
		t.Fatalf("expected the refusal alone, got %v", got)
	}
	if got := i18n.Of("de", err); !strings.HasPrefix(got, "Die Adresse ") {
		t.Fatalf("expected a German refusal, got %q", got)
	}
}

func TestSafeClientBlocksLinkLocalLiteral(t *testing.T) {
	netguard.SetAllowPrivateHosts(true)
	t.Cleanup(func() { netguard.SetAllowPrivateHosts(false) })

	_, err := NewClient("http://169.254.169.254/", "k", nil)
	if err == nil || !strings.Contains(err.Error(), "never allowed") {
		t.Fatalf("expected link-local URL to be rejected, got %v", err)
	}
}

func TestSafeClientBlocksRedirectToLinkLocal(t *testing.T) {
	netguard.SetAllowPrivateHosts(true)
	t.Cleanup(func() { netguard.SetAllowPrivateHosts(false) })

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
	netguard.SetAllowPrivateHosts(true)
	t.Cleanup(func() { netguard.SetAllowPrivateHosts(false) })

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
