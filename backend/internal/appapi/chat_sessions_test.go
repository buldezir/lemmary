package appapi

import (
	"net/url"
	"strings"
	"testing"

	"lemmary/backend/internal/chat"
)

func values(raw string) url.Values {
	parsed, err := url.ParseQuery(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestParseChatListQueryDefaults(t *testing.T) {
	q, page, perPage, err := parseChatListQuery(values(""), "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page != 1 || perPage != defaultListPageSize {
		t.Fatalf("page=%d perPage=%d", page, perPage)
	}
	if q.UserID != "user1" || q.Kind != "" || q.DocumentID != "" {
		t.Fatalf("unexpected query: %+v", q)
	}
	if q.Offset != 0 || q.Limit != defaultListPageSize {
		t.Fatalf("offset=%d limit=%d", q.Offset, q.Limit)
	}
}

func TestParseChatListQueryFilters(t *testing.T) {
	q, _, _, err := parseChatListQuery(values("kind=document&document=doc7"), "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Kind != chat.KindDocument {
		t.Fatalf("kind = %q", q.Kind)
	}
	if q.DocumentID != "doc7" {
		t.Fatalf("document = %q", q.DocumentID)
	}
}

func TestParseChatListQueryRejectsUnknownKind(t *testing.T) {
	_, _, _, err := parseChatListQuery(values("kind=elsewhere"), "user1")
	if err == nil {
		t.Fatal("expected an error for an unknown kind")
	}
	if !strings.Contains(err.Error(), "elsewhere") {
		t.Fatalf("error should name the value: %v", err)
	}
}

func TestParseChatListQueryClampsPerPage(t *testing.T) {
	_, _, perPage, err := parseChatListQuery(values("perPage=100000"), "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if perPage != maxListPageSize {
		t.Fatalf("perPage = %d, want %d", perPage, maxListPageSize)
	}
}

// A junk or negative page must fall back rather than producing a negative
// offset, which would make the query fail rather than return page one.
func TestParseChatListQueryIgnoresJunkPaging(t *testing.T) {
	for _, raw := range []string{"page=0", "page=-3", "page=abc"} {
		q, page, _, err := parseChatListQuery(values(raw), "user1")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", raw, err)
		}
		if page != 1 || q.Offset != 0 {
			t.Errorf("%s: page=%d offset=%d", raw, page, q.Offset)
		}
	}
}

func TestParseChatListQueryOffset(t *testing.T) {
	q, page, perPage, err := parseChatListQuery(values("page=3&perPage=10"), "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page != 3 || perPage != 10 || q.Offset != 20 || q.Limit != 10 {
		t.Fatalf("page=%d perPage=%d offset=%d limit=%d", page, perPage, q.Offset, q.Limit)
	}
}
