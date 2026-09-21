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
	"lemmary/backend/internal/models"
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
		60: "0 */1 * * *", 180: "0 */3 * * *", 1380: "0 */23 * * *", 1440: "0 0 * * *",
	} {
		if got := cronExpr(minutes); got != want {
			t.Errorf("cronExpr(%d) = %q, want %q", minutes, got, want)
		}
	}
}

// A documents cap is room every later file would also lack, so the scan stops
// rather than burning through the folder; the file is not remembered as refused,
// because it is not the file that was wrong.
func TestScanStopsAtTheInstanceLimitAndRetriesLater(t *testing.T) {
	app := testpb.Open(t)
	limits.Register(app, limits.Limits{Documents: limits.Of(1)})
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
	s := &Scanner{app: app, dir: t.TempDir(), seen: map[string]fileStamp{}}
	drop(t, s.dir, "a.txt", "first")
	drop(t, s.dir, "b.txt", "second")
	drop(t, s.dir, "c.txt", "third")

	res := s.Scan(config.Config{}, time.Now())
	if res.Created != 1 || res.Failed != 0 {
		t.Fatalf("scan under a one-document cap = %+v, want 1 created and a stop", res)
	}
	if len(s.seen) != 0 {
		t.Fatalf("seen = %v; a room limit must not mark files as refused", s.seen)
	}
}

// Oversized files and symlinks never reach the create hooks: the first would
// be buffered whole before PocketBase refused it, the second could point
// anywhere on the host by the time the walk's entry is opened.
func TestScanRefusesOversizedFilesAndSymlinks(t *testing.T) {
	s, ownerID := newScanner(t)
	huge := filepath.Join(s.dir, "huge.txt")
	if err := os.WriteFile(huge, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(huge, models.MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(huge, old, old); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegular(filepath.Join(s.dir, "link.txt")); err == nil {
		t.Fatal("readRegular followed a symlink")
	}

	res := s.Scan(config.Config{IngestDirDeleteOriginal: true}, time.Now())
	if res.Created != 0 || res.Failed != 1 {
		t.Fatalf("scan = %+v, want the oversized file failed and the symlink ignored", res)
	}
	if len(ingestedFor(t, s.app, ownerID)) != 0 {
		t.Fatal("something was imported")
	}
	for _, name := range []string{"huge.txt", "link.txt"} {
		if _, err := os.Lstat(filepath.Join(s.dir, name)); err != nil {
			t.Fatalf("%s must survive a refusal even in delete mode: %v", name, err)
		}
	}
}

// The seen cache answers for one owner in one mode. Changing either has to
// look at the folder afresh: a new owner has none of the files, and delete mode
// owes the folder a clean-up of what keep mode left behind.
func TestSeenCacheResetsWhenOwnerOrDeleteModeChanges(t *testing.T) {
	s, ownerID := newScanner(t)
	users, err := s.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	other := core.NewRecord(users)
	other.Set("email", "other@example.com")
	other.SetPassword("test-password-123")
	if err := s.app.Save(other); err != nil {
		t.Fatal(err)
	}
	path := drop(t, s.dir, "a.txt", "shared content")

	s.Scan(config.Config{}, time.Now())
	if res := s.Scan(config.Config{}, time.Now()); res.Skipped != 1 {
		t.Fatalf("second keep-mode scan = %+v, want the duplicate skipped", res)
	}
	if res := s.Scan(config.Config{IngestDirOwner: other.Id}, time.Now()); res.Created != 1 {
		t.Fatalf("scan for a new owner = %+v, want the file imported for them", res)
	}
	if len(ingestedFor(t, s.app, ownerID)) != 1 || len(ingestedFor(t, s.app, other.Id)) != 1 {
		t.Fatal("each owner should hold one copy")
	}
	if res := s.Scan(config.Config{IngestDirOwner: other.Id, IngestDirDeleteOriginal: true}, time.Now()); res.Skipped != 1 {
		t.Fatalf("delete-mode scan = %+v, want the duplicate seen again and removed", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should be removed once delete mode is on, stat err = %v", err)
	}
}

func ingestedFor(t *testing.T, app core.App, ownerID string) []*core.Record {
	t.Helper()
	docs, err := app.FindRecordsByFilter("documents", "user = {:user}", "created", 0, 0, map[string]any{"user": ownerID})
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func TestScanDoesNothingWithoutAnOwner(t *testing.T) {
	app := testpb.Open(t)
	s := &Scanner{app: app, dir: t.TempDir(), seen: map[string]fileStamp{}}
	drop(t, s.dir, "a.txt", "orphan")
	if res := s.Scan(config.Config{}, time.Now()); res != (Result{}) {
		t.Fatalf("scan before setup = %+v, want nothing", res)
	}
}
