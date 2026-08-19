package worker

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestEnsureNamedEntityRequiresUser(t *testing.T) {
	_, created, err := EnsureNamedEntity(nil, "correspondents", "", "Acme", "Acme")
	if err == nil {
		t.Fatal("expected error when user id is empty")
	}
	if created {
		t.Fatal("should not create without a user id")
	}
}

func TestEnsureNamedEntitySkipsEmptyName(t *testing.T) {
	id, created, err := EnsureNamedEntity(nil, "correspondents", "user1", "  ", "")
	if err != nil {
		t.Fatalf("empty name should be a no-op, got %v", err)
	}
	if id != "" || created {
		t.Fatalf("expected empty result, id=%q created=%v", id, created)
	}
}

func TestListNamedEntityNamesEmptyUser(t *testing.T) {
	for _, fn := range []func(core.App, string) ([]string, error){
		listCorrespondentNames,
		listDocumentTypeNames,
	} {
		names, err := fn(nil, "  ")
		if err != nil {
			t.Fatalf("empty user should be a no-op, got %v", err)
		}
		if len(names) != 0 {
			t.Fatalf("expected no names, got %v", names)
		}
	}
}

func TestLoadExtractionCatalogEmptyUser(t *testing.T) {
	catalog := loadExtractionCatalog(nil, "  ", slog.Default())
	if len(catalog.Correspondents) != 0 || len(catalog.DocumentTypes) != 0 {
		t.Fatalf("expected empty catalog, got %+v", catalog)
	}
}

func TestNormalizeNamedEntityKey(t *testing.T) {
	if got, want := normalizeNamedEntityKey("Amazon EU S.à r.l."), normalizeNamedEntityKey("Amazon EU S.a.r.l."); got != want || got == "" {
		t.Fatalf("accent/punct variants should match, got %q vs %q", got, want)
	}
	if got, want := normalizeNamedEntityKey("Invoice"), normalizeNamedEntityKey("invoice"); got != want {
		t.Fatalf("case variants should match, got %q vs %q", got, want)
	}
	branch := normalizeNamedEntityKey("Amazon EU S.à r.l., German Branch")
	base := normalizeNamedEntityKey("Amazon EU S.à r.l.")
	if branch == base {
		t.Fatalf("German Branch should stay distinct, both %q", branch)
	}
	if normalizeNamedEntityKey("...") != "" {
		t.Fatal("punctuation-only names should normalize empty")
	}
}

func TestAddUniqueCatalogNameCaps(t *testing.T) {
	seen := map[string]struct{}{}
	var names []string
	for i := 0; i < 10; i++ {
		names = addUniqueCatalogName(names, seen, fmt.Sprintf("Name %d", i), 3)
	}
	if len(names) != 3 {
		t.Fatalf("len=%d want 3: %v", len(names), names)
	}
	names = addUniqueCatalogName(names, seen, "Name 0", 3)
	if len(names) != 3 {
		t.Fatalf("duplicate should not grow list: %v", names)
	}
}

func TestRequireOwnedRelationSkipsEmpty(t *testing.T) {
	if err := requireOwnedRelation(nil, "correspondents", "correspondent", "", "user1"); err != nil {
		t.Fatalf("empty id should skip, got %v", err)
	}
	if err := requireOwnedRelation(nil, "correspondents", "correspondent", "  ", "user1"); err != nil {
		t.Fatalf("blank id should skip, got %v", err)
	}
}
