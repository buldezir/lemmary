package remoteimport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestPapraClient(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ppapi_key" {
			t.Errorf("Authorization=%q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []papraOrganization{{ID: "org_1", Name: "Home"}}})
	})
	mux.HandleFunc("GET /api/organizations/org_1/documents", func(w http.ResponseWriter, r *http.Request) {
		pageIndex, _ := strconv.Atoi(r.URL.Query().Get("pageIndex"))
		size := papraPageSize
		if pageIndex == 1 {
			size = 1
		}
		docs := make([]papraDocument, size)
		for i := range docs {
			docs[i] = papraDocument{ID: fmt.Sprintf("doc_%d_%d", pageIndex, i)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"documents": docs, "documentsCount": papraPageSize + 1})
	})
	mux.HandleFunc("GET /api/organizations/org_1/documents/doc_0_0", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"document":{"id":"doc_0_0","name":"Invoice.pdf","content":"OCR text","documentDate":"2024-06-14T22:00:00.000Z","tags":[{"id":"tag_1","name":"Bills"}]}}`))
	})
	mux.HandleFunc("GET /api/organizations/org_1/documents/doc_0_0/file", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''Rechnung%20M%C3%A4rz.pdf`)
		_, _ = w.Write([]byte("%PDF"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := newPapraClient(srv.URL+"/api/", " ppapi_key ", srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	orgs, err := client.papraOrganizations()
	if err != nil || len(orgs) != 1 || orgs[0].ID != "org_1" {
		t.Fatalf("orgs=%v err=%v", orgs, err)
	}

	total := 0
	if err := client.forEachPapraDocuments("org_1", func(docs []papraDocument) error {
		total += len(docs)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if total != papraPageSize+1 {
		t.Fatalf("total=%d", total)
	}

	doc, err := client.papraDocument("org_1", "doc_0_0")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Content != "OCR text" || len(doc.Tags) != 1 || doc.Tags[0].Name != "Bills" {
		t.Fatalf("doc=%+v", doc)
	}

	file, err := client.downloadPapraDocument("org_1", "doc_0_0")
	if err != nil {
		t.Fatal(err)
	}
	if file.Name != "Rechnung März.pdf" || string(file.Data) != "%PDF" {
		t.Fatalf("file=%+v", file)
	}
}

func TestPapraDate(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		// Picked on the calendar: local midnight, by offset.
		"2024-06-15T00:00:00.000Z": "2024-06-15", // UTC
		"2024-06-14T22:00:00.000Z": "2024-06-15", // +2
		"2024-06-14T18:30:00.000Z": "2024-06-15", // +5:30
		"2024-06-14T12:00:00.000Z": "2024-06-15", // +12
		"2024-06-14T11:00:00.000Z": "2024-06-15", // +13
		"2024-06-14T10:00:00.000Z": "2024-06-15", // +14
		"2024-06-15T05:00:00.000Z": "2024-06-15", // −5
		"2024-06-15T09:00:00.000Z": "2024-06-15", // −9
		// Today: the instant of the press.
		"2024-06-15T13:00:04.217Z": "2024-06-15", // 15:00 in Berlin
		"2024-06-15T17:12:45.003Z": "2024-06-15", // 10:12 in California
		// An offset in the value, or no date at all.
		"2024-06-15T00:00:00.000+02:00": "2024-06-15",
		"":                              "",
		"not a date":                    "",
	}
	for in, want := range cases {
		if got := papraDate(in); got != want {
			t.Errorf("papraDate(%q)=%q want %q", in, got, want)
		}
	}
}
