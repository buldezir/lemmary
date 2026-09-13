package worker

import (
	"fmt"
	"log/slog"
	"sync"
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

// Only (user, name) is unique in the schema, so the normalized tier is unbacked
// by an index: two pipelines extracting spelling variants of the same
// correspondent at the same time would both miss, both insert, and no conflict
// would fire for reuseNamedEntityAfterConflict to clean up. With several
// documents in flight at once that is an everyday occurrence, not a race to
// shrug at.
func TestEnsureNamedEntityConcurrentSpellingVariants(t *testing.T) {
	app := bootAppForEnqueue(t)
	userID := makeUserForDrain(t, app, "entities@example.test")

	names := []string{"Müller GmbH", "Muller G.m.b.H.", "MÜLLER  GMBH", "müller gmbh"}
	ids := make([]string, len(names))
	errs := make([]error, len(names))

	// A barrier, so the goroutines are actually inside the lookup at the same
	// moment rather than starting a comfortable distance apart.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ids[i], _, errs[i] = EnsureNamedEntity(app, "correspondents", userID, name, name)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("ensure %q: %v", names[i], err)
		}
	}
	for i, id := range ids {
		if id == "" {
			t.Fatalf("ensure %q returned no id", names[i])
		}
		if id != ids[0] {
			t.Fatalf("%q landed on %s but %q on %s: one correspondent split in two",
				names[i], id, names[0], ids[0])
		}
	}

	records, err := app.FindRecordsByFilter("correspondents", "user = {:user}", "", 0, 0,
		map[string]any{"user": userID})
	if err != nil {
		t.Fatalf("list correspondents: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one correspondent, got %d", len(records))
	}
}
