package ngxapi

import (
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/fulltext"
)

func TestClampSearchPageSize(t *testing.T) {
	if got := clampSearchPageSize(1000); got != fulltext.MaxSearchLimit {
		t.Fatalf("clampSearchPageSize(1000)=%d, want %d", got, fulltext.MaxSearchLimit)
	}
	if got := clampSearchPageSize(25); got != 25 {
		t.Fatalf("clampSearchPageSize(25)=%d, want 25", got)
	}
}

func TestPaginationParamsThenClampForSearch(t *testing.T) {
	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest("GET", "/api/documents/?query=invoice&page=2&page_size=1000", nil)

	page, pageSize := paginationParams(e)
	pageSize = clampSearchPageSize(pageSize)
	if page != 2 {
		t.Fatalf("page=%d", page)
	}
	if pageSize != fulltext.MaxSearchLimit {
		t.Fatalf("pageSize=%d, want %d", pageSize, fulltext.MaxSearchLimit)
	}
	offset := (page - 1) * pageSize
	if offset != fulltext.MaxSearchLimit {
		t.Fatalf("offset=%d would skip hits; want %d", offset, fulltext.MaxSearchLimit)
	}
}
