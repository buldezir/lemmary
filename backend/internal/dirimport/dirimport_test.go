package dirimport

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/testpb"
	_ "lemmary/backend/migrations"
)

func newScanner(t *testing.T) (*Scanner, string) {
	t.Helper()
	app := testpb.Open(t)
	// The create hooks are what hash and size a document; without them the
	// walk would be tested against a schema, not the app.
	limits.Register(app, limits.Limits{})
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	admin := core.NewRecord(users)
	admin.Set("email", "admin@example.com")
	admin.Set("is_app_admin", true)
	admin.SetPassword("test-password-123")
	if err := app.Save(admin); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	return &Scanner{app: app, dir: dir, seen: map[string]fileStamp{}}, admin.Id
}

// Written with an mtime in the past so the settle check lets it through.
func drop(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func tagNames(t *testing.T, app core.App, doc *core.Record) []string {
	t.Helper()
	var names []string
	for _, id := range doc.GetStringSlice("tags") {
		tag, err := app.FindRecordById("tags", id)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tag.GetString("name"))
	}
	return names
}

func TestScanCreatesDocumentsWithFolderTagsAndSkipsWhatItHasSeen(t *testing.T) {
	s, ownerID := newScanner(t)
	drop(t, s.dir, "a.txt", "first document")
	drop(t, s.dir, "Taxes/2024/b.txt", "second document")
	drop(t, s.dir, "taxes/c.txt", "third document")
	drop(t, s.dir, ".hidden/d.txt", "never")
	drop(t, s.dir, "notes.exe", "not storable")
	if err := os.WriteFile(filepath.Join(s.dir, "fresh.txt"), []byte("still being written"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := s.Scan(config.Config{}, time.Now())
	if res.Created != 3 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("first scan = %+v, want 3 created", res)
	}

	docs, err := s.app.FindRecordsByFilter("documents", "user = {:user}", "created", 0, 0, map[string]any{"user": ownerID})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("documents = %d", len(docs))
	}
	for _, doc := range docs {
		if doc.GetString("checksum") == "" {
			t.Fatalf("document %s has no checksum", doc.Id)
		}
	}
	tags, err := s.app.FindRecordsByFilter("tags", "user = {:user}", "name", 0, 0, map[string]any{"user": ownerID})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tag := range tags {
		names = append(names, tag.GetString("name"))
	}
	if len(names) != 2 || names[0] != "2024" || names[1] != "Taxes" {
		t.Fatalf("tags = %v, want [2024 Taxes]: taxes/ must reuse Taxes", names)
	}
	// Tag lists are sorted by depth, so the deepest file has two.
	var perDoc []string
	for _, doc := range docs {
		perDoc = append(perDoc, strings.Join(tagNames(t, s.app, doc), "/"))
	}
	sort.Strings(perDoc)
	if want := []string{"", "Taxes", "Taxes/2024"}; !slices.Equal(perDoc, want) {
		t.Fatalf("tags per document = %q, want %q", perDoc, want)
	}

	// Keep mode: the same files are already in the library and stay on disk.
	res = s.Scan(config.Config{}, time.Now())
	if res.Created != 0 || res.Skipped != 3 {
		t.Fatalf("second scan = %+v, want 3 skipped", res)
	}
	res = s.Scan(config.Config{}, time.Now())
	if res.Created != 0 || res.Skipped != 0 {
		t.Fatalf("third scan = %+v, want nothing touched (seen cache)", res)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "a.txt")); err != nil {
		t.Fatalf("a.txt should still exist in keep mode: %v", err)
	}
}

func TestScanDeletesOriginalsWhenAsked(t *testing.T) {
	s, _ := newScanner(t)
	a := drop(t, s.dir, "a.txt", "first document")
	b := drop(t, s.dir, "dup.txt", "first document")

	res := s.Scan(config.Config{IngestDirDeleteOriginal: true}, time.Now())
	if res.Created != 1 || res.Skipped != 1 {
		t.Fatalf("scan = %+v, want 1 created and 1 duplicate skipped", res)
	}
	for _, path := range []string{a, b} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s should have been removed, stat err = %v", path, err)
		}
	}
}

func TestCronExprFollowsTheInterval(t *testing.T) {
	for minutes, want := range map[int]string{
		0: "* * * * *", 1: "* * * * *", 5: "*/5 * * * *", 59: "*/59 * * * *",
		60: "0 */1 * * *", 180: "0 */3 * * *", 100000: "0 */23 * * *",
	} {
		if got := cronExpr(minutes); got != want {
			t.Errorf("cronExpr(%d) = %q, want %q", minutes, got, want)
		}
	}
}

func TestScanDoesNothingWithoutAnOwner(t *testing.T) {
	app := testpb.Open(t)
	s := &Scanner{app: app, dir: t.TempDir(), seen: map[string]fileStamp{}}
	drop(t, s.dir, "a.txt", "orphan")
	if res := s.Scan(config.Config{}, time.Now()); res != (Result{}) {
		t.Fatalf("scan before setup = %+v, want nothing", res)
	}
}
