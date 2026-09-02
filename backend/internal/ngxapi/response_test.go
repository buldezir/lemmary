package ngxapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/core/validators"
)

// TestSaveErrorMapsPocketBaseValidation guards the type identity between the
// ozzo-validation fork PocketBase returns and the one saveError matches on.
// PocketBase moved from go-ozzo to its own fork in v0.40; because the two
// declare distinct Go types, importing the wrong one still compiles but turns
// every client-caused validation failure into a generic 500.
func TestSaveErrorMapsPocketBaseValidation(t *testing.T) {
	t.Parallel()

	pbErr := validators.NormalizeUniqueIndexError(
		errors.New("UNIQUE constraint failed: documents.checksum"),
		"documents",
		[]string{"checksum"},
	)

	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Response = rec
	e.Request = httptest.NewRequest(http.MethodPost, "/api/documents/", nil)

	if err := saveError(e, pbErr); err != nil {
		t.Fatalf("saveError() error = %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("saveError() status = %d, want %d (PocketBase validation error not recognised)", rec.Code, http.StatusBadRequest)
	}
}
