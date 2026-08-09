package ngximport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://ngx.example.com", "https://ngx.example.com", false},
		{"https://ngx.example.com/", "https://ngx.example.com", false},
		{"https://ngx.example.com/api", "https://ngx.example.com", false},
		{"https://ngx.example.com/api/", "https://ngx.example.com", false},
		{"http://127.0.0.1:8000/paperless/api", "http://127.0.0.1:8000/paperless", false},
		{"", "", true},
		{"ftp://x", "", true},
		{"not-a-url", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeBaseURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("NormalizeBaseURL(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NormalizeBaseURL(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("NormalizeBaseURL(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestClientListAndDownload(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Token secret-key" {
			t.Fatalf("Authorization=%q", got)
		}
		writePage(w, []namedEntity{{ID: 1, Name: "Invoice"}})
	})
	mux.HandleFunc("/api/correspondents/", func(w http.ResponseWriter, r *http.Request) {
		writePage(w, []namedEntity{{ID: 2, Name: "Acme"}})
	})
	mux.HandleFunc("/api/document_types/", func(w http.ResponseWriter, r *http.Request) {
		writePage(w, []namedEntity{{ID: 3, Name: "Receipt"}})
	})
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/download") {
			w.Header().Set("Content-Disposition", `attachment; filename="invoice.txt"`)
			_, _ = w.Write([]byte("hello invoice"))
			return
		}
		corr := 2
		dtype := 3
		writePage(w, []ngxDocument{{
			ID:               10,
			Title:            "Invoice 1",
			Content:          "OCR text here",
			Tags:             []int{1},
			Correspondent:    &corr,
			DocumentType:     &dtype,
			CreatedDate:      "2024-05-01",
			OriginalFileName: "invoice.txt",
		}})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewClient(srv.URL, "secret-key", srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	tags, err := client.ListTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Name != "Invoice" {
		t.Fatalf("tags=%v", tags)
	}

	docs, err := client.ListDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Content != "OCR text here" {
		t.Fatalf("docs=%v", docs)
	}

	file, err := client.DownloadDocument(10)
	if err != nil {
		t.Fatal(err)
	}
	if file.Name != "invoice.txt" || string(file.Data) != "hello invoice" {
		t.Fatalf("file=%+v", file)
	}
}

func TestClientPagination(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			next := "/api/tags/?page=2&page_size=100"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count":    2,
				"next":     next,
				"previous": nil,
				"results":  []namedEntity{{ID: 1, Name: "A"}},
			})
			return
		}
		writePage(w, []namedEntity{{ID: 2, Name: "B"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewClient(srv.URL+"/api", "k", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	tags, err := client.ListTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("len=%d", len(tags))
	}
}

func TestDocumentDate(t *testing.T) {
	t.Parallel()
	corr := 1
	got := documentDate(ngxDocument{CreatedDate: "2024-01-15", Correspondent: &corr})
	if got != "2024-01-15" {
		t.Fatalf("got %q", got)
	}
	got = documentDate(ngxDocument{Created: "2024-02-03T10:00:00Z"})
	if got != "2024-02-03" {
		t.Fatalf("got %q", got)
	}
}

func writePage[T any](w http.ResponseWriter, results []T) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count":    len(results),
		"next":     nil,
		"previous": nil,
		"results":  results,
	})
}
