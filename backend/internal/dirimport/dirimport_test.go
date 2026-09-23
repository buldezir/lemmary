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
	"github.com/pocketbase/pocketbase/tools/router"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/inflight"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/testpb"
	_ "lemmary/backend/migrations"
)

// The create hooks are what hash and size a document; without them the walk
// would be tested against a schema, not the app.
func openApp(t *testing.T, lim limits.Limits) core.App {
	t.Helper()
	app := testpb.Open(t)
	limits.Register(app, lim)
	app.OnRecordCreate("documents").BindFunc(func(e *core.RecordEvent) error {
		if err := duplicates.AssignChecksumFromUpload(e.App, e.Record); err != nil {
			return err
		}
		return e.Next()
	})
	return app
}

func createAdmin(t *testing.T, app core.App) string {
	t.Helper()
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
	return admin.Id
}

func scannerWithAdmin(t *testing.T) (*Scanner, string) {
	t.Helper()
	app := openApp(t, limits.Limits{})
	ownerID := createAdmin(t, app)
	return newScanner(app, nil, t.TempDir()), ownerID
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
	s, ownerID := scannerWithAdmin(t)
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

	// Keep mode: the files stay on disk and are not looked at again.
	res = s.Scan(config.Config{}, time.Now())
	if res != (Result{}) {
		t.Fatalf("second scan = %+v, want nothing touched", res)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "a.txt")); err != nil {
		t.Fatalf("a.txt should still exist in keep mode: %v", err)
	}
}

func TestScanDeletesOriginalsWhenAsked(t *testing.T) {
	s, _ := scannerWithAdmin(t)
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

// A documents cap is room every later file would also lack, so the scan stops
// rather than burning through the folder; the file is not remembered as refused,
// because it is not the file that was wrong.
func TestScanStopsAtTheInstanceLimitAndRetriesLater(t *testing.T) {
	app := openApp(t, limits.Limits{Documents: limits.Of(1)})
	createAdmin(t, app)
	s := newScanner(app, nil, t.TempDir())
	drop(t, s.dir, "a.txt", "first")
	drop(t, s.dir, "b.txt", "second")
	drop(t, s.dir, "c.txt", "third")

	res := s.Scan(config.Config{}, time.Now())
	if res.Created != 1 || res.Failed != 0 {
		t.Fatalf("scan under a one-document cap = %+v, want 1 created and a stop", res)
	}
	if len(s.seen) != 1 {
		t.Fatalf("seen = %v; only the imported file, a room limit must not mark files as refused", s.seen)
	}
}

// Oversized files and symlinks never reach the create hooks: the first would
// be buffered whole before PocketBase refused it, the second could point
// anywhere on the host by the time the walk's entry is opened.
func TestScanRefusesOversizedFilesAndSymlinks(t *testing.T) {
	s, ownerID := scannerWithAdmin(t)
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
	if _, err := regularFile(filepath.Join(s.dir, "link.txt")).Open(); err == nil {
		t.Fatal("regularFile followed a symlink")
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
	s, ownerID := scannerWithAdmin(t)
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

	if res := s.Scan(config.Config{}, time.Now()); res.Created != 1 {
		t.Fatalf("keep-mode scan = %+v, want the file imported", res)
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
	s := newScanner(openApp(t, limits.Limits{}), nil, t.TempDir())
	drop(t, s.dir, "a.txt", "orphan")
	if res := s.Scan(config.Config{}, time.Now()); res != (Result{}) {
		t.Fatalf("scan before setup = %+v, want nothing", res)
	}
}

// Keep mode remembers what it consumed in the database, so neither a restart
// nor deleting the document brings the file back; changing the file does.
func TestKeepModeLedgerOutlivesTheProcessAndTheDocument(t *testing.T) {
	s, ownerID := scannerWithAdmin(t)
	path := drop(t, s.dir, "a.txt", "kept in the folder")
	if res := s.Scan(config.Config{}, time.Now()); res.Created != 1 {
		t.Fatalf("first scan = %+v, want 1 created", res)
	}
	for _, doc := range ingestedFor(t, s.app, ownerID) {
		if err := s.app.Delete(doc); err != nil {
			t.Fatal(err)
		}
	}

	restarted := newScanner(s.app, nil, s.dir)
	if res := restarted.Scan(config.Config{}, time.Now()); res != (Result{}) {
		t.Fatalf("scan after restart = %+v, want the deleted document left deleted", res)
	}

	if err := os.WriteFile(path, []byte("edited since"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if res := restarted.Scan(config.Config{}, time.Now()); res.Created != 1 {
		t.Fatalf("scan after an edit = %+v, want the new content imported", res)
	}
}

// With encryption at rest the original is the only durable copy until the
// vault has sealed the new document, so delete mode waits for that.
func TestDeleteModeWaitsUntilTheDocumentIsSealed(t *testing.T) {
	s, _ := scannerWithAdmin(t)
	seal := inflight.NewSeal()
	s.app.Store().Set(inflight.SealStoreKey, seal)
	s.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		seal.Wrote()
		return e.Next()
	})
	path := drop(t, s.dir, "a.txt", "only copy")

	cfg := config.Config{IngestDirDeleteOriginal: true}
	if res := s.Scan(cfg, time.Now()); res.Created != 1 {
		t.Fatalf("scan = %+v, want 1 created", res)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("original removed before its document was sealed: %v", err)
	}
	seal.Sealed(time.Now().UnixNano())
	if res := s.Scan(cfg, time.Now()); res != (Result{}) {
		t.Fatalf("second scan = %+v, want the pending original not re-imported", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original should be gone once sealed, stat err = %v", err)
	}
}

func TestScanFollowsASymlinkedRoot(t *testing.T) {
	s, _ := scannerWithAdmin(t)
	drop(t, s.dir, "a.txt", "behind a link")
	link := filepath.Join(t.TempDir(), "consume")
	if err := os.Symlink(s.dir, link); err != nil {
		t.Fatal(err)
	}
	s.dir = link
	if res := s.Scan(config.Config{}, time.Now()); res.Created != 1 {
		t.Fatalf("scan through a symlinked root = %+v, want 1 created", res)
	}
}

// Folder tags are made for the document; one that is refused takes them back.
func TestRefusedFileLeavesNoFolderTags(t *testing.T) {
	s, ownerID := scannerWithAdmin(t)
	s.app.OnRecordCreate("documents").BindFunc(func(e *core.RecordEvent) error {
		return router.NewBadRequestError("refused", nil)
	})
	drop(t, s.dir, "Weird/Folder/bad.txt", "refused by a create hook")
	if res := s.Scan(config.Config{}, time.Now()); res.Failed != 1 {
		t.Fatalf("scan = %+v, want the file refused", res)
	}
	tags, err := s.app.FindRecordsByFilter("tags", "user = {:user}", "", 0, 0, map[string]any{"user": ownerID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Fatalf("%d tags left behind by a refused file", len(tags))
	}
}
