package vault

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/crypt"
)

// fakeSnapshotter stands in for VACUUM INTO so the storage engine can be tested
// without a PocketBase instance.
type fakeSnapshotter struct{ bodies map[string][]byte }

func (f *fakeSnapshotter) SnapshotDatabases(stage string) error {
	for name, body := range f.bodies {
		if err := os.WriteFile(filepath.Join(stage, name), body, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// cheapKeyring builds a keyring at the lowest Argon2 cost Validate accepts, so
// the engine suite is not dominated by key derivation.
func cheapKeyring(t *testing.T) (*Keyring, crypt.Key) {
	t.Helper()
	mk, err := crypt.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	salt, err := newSalt()
	if err != nil {
		t.Fatalf("newSalt: %v", err)
	}
	kr := &Keyring{Version: keyringVersion, Salt: salt, MKFP: crypt.KeyID(mk)}

	params, err := crypt.NewKDFParams()
	if err != nil {
		t.Fatalf("NewKDFParams: %v", err)
	}
	params.MemKiB, params.Time, params.Lanes = 8*1024, 1, 1
	kek, err := crypt.DeriveKEK("test-password", params)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	w := Wrap{ID: wrapID("u1", "pw"), User: "u1", Type: WrapPassword, KDF: &params}
	if err := kr.sealInto(&w, kek, mk); err != nil {
		t.Fatalf("sealInto: %v", err)
	}
	return kr, mk
}

type harness struct {
	t       *testing.T
	v       *Vault
	dir     string
	workDir string
	kr      *Keyring
	mk      crypt.Key
	logs    []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{t: t, dir: filepath.Join(root, "vault"), workDir: filepath.Join(root, "work")}
	h.kr, h.mk = cheapKeyring(t)
	h.open()
	if err := h.kr.Save(h.dir); err != nil {
		t.Fatalf("Save keyring: %v", err)
	}
	if err := h.v.adopt(h.mk); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	return h
}

// open (re)creates the Vault against the same on-disk state, as a restart would.
func (h *harness) open() {
	h.t.Helper()
	if h.v != nil {
		h.v.releaseLock()
	}
	v, err := New(Options{
		Dir:     h.dir,
		WorkDir: h.workDir,
		Enabled: true,
		// t.TempDir() is ordinary storage; production requires tmpfs and is
		// covered by TestWorkDirMustBeMemoryBacked.
		AllowDiskWorkDir: true,
		Log:              func(f string, a ...any) { h.logs = append(h.logs, fmt.Sprintf(f, a...)) },
	})
	if err != nil {
		h.t.Fatalf("New: %v", err)
	}
	v.SetSnapshotter(&fakeSnapshotter{bodies: map[string][]byte{
		"data.db":      []byte("SQLite format 3\x00 pretend database with ocr text"),
		"auxiliary.db": []byte("SQLite format 3\x00 logs"),
	}})
	if v.kr == nil {
		v.kr = h.kr
	}
	h.v = v
}

// reopen simulates a process restart: drop the vault, wipe the working
// directory, and unlock again from the persistent state alone.
func (h *harness) reopen() {
	h.t.Helper()
	h.v.releaseLock()
	if err := os.RemoveAll(h.workDir); err != nil {
		h.t.Fatalf("wipe work dir: %v", err)
	}
	h.open()
	if err := h.v.adopt(h.mk); err != nil {
		h.t.Fatalf("adopt after restart: %v", err)
	}
}

func (h *harness) write(rel, body string) {
	h.t.Helper()
	p := filepath.Join(h.workDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		h.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		h.t.Fatalf("write %s: %v", rel, err)
	}
}

func (h *harness) read(rel string) string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.workDir, filepath.FromSlash(rel)))
	if err != nil {
		h.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// tree hashes the working directory so two states can be compared exactly.
func (h *harness) tree() map[string]string {
	h.t.Helper()
	out := map[string]string{}
	err := filepath.Walk(h.workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(h.workDir, path)
		if isExcluded(rel) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		h.t.Fatalf("walk: %v", err)
	}
	return out
}

func (h *harness) blobCount() int {
	h.t.Helper()
	n := 0
	_ = filepath.Walk(filepath.Join(h.dir, blobsDirName), func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

func TestFlushRestoreRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.write("storage/pbc_1/rec1/invoice.pdf", "%PDF-1.7 confidential invoice body")
	h.write("storage/pbc_1/rec1/invoice_preview.png", "\x89PNG fake preview")
	h.write("storage/_pb_users_auth_/u1/avatar.png", "\x89PNG avatar")

	if err := h.v.Flush("test"); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	before := h.tree()
	if h.v.Generation() != 1 {
		t.Fatalf("generation = %d, want 1", h.v.Generation())
	}

	h.reopen()

	after := h.tree()
	if len(after) != len(before) {
		t.Fatalf("restored %d files, had %d", len(after), len(before))
	}
	for path, sum := range before {
		if after[path] != sum {
			t.Fatalf("%s differs after restore", path)
		}
	}
	if got := h.read("storage/pbc_1/rec1/invoice.pdf"); got != "%PDF-1.7 confidential invoice body" {
		t.Fatalf("document body corrupted: %q", got)
	}
	// The snapshotted databases come back under their canonical names.
	if !strings.HasPrefix(h.read("data.db"), "SQLite format 3") {
		t.Fatal("data.db was not restored")
	}
}

// The property that matters most: after a flush, nothing readable is left on the
// persistent volume.
func TestNoPlaintextReachesTheVolume(t *testing.T) {
	h := newHarness(t)
	const marker = "MARKER-a1b2c3-CONFIDENTIAL"

	h.write("storage/pbc_1/rec1/"+marker+".pdf", "%PDF-1.7 body containing "+marker)
	h.write("storage/pbc_1/rec2/plain.txt", strings.Repeat("A", 4096))
	h.v.SetSnapshotter(&fakeSnapshotter{bodies: map[string][]byte{
		"data.db": []byte("SQLite format 3\x00 ocr_text=" + marker),
	}})
	if err := h.v.Flush("test"); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var checked int
	err := filepath.Walk(h.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(h.dir, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++

		// The keyring is the one file that is legitimately cleartext, but it
		// must still carry no user content.
		if bytes.Contains(b, []byte(marker)) {
			t.Fatalf("%s contains the plaintext marker", rel)
		}
		if rel == keyringName || rel == currentName || rel == lockName {
			return nil
		}
		for _, magic := range []string{"%PDF-", "SQLite format 3\x00", "\x89PNG", "PK\x03\x04"} {
			if bytes.Contains(b, []byte(magic)) {
				t.Fatalf("%s contains the %q file signature", rel, magic)
			}
		}
		// Even a 4 KiB run of identical bytes must not survive encryption.
		run, last := 1, byte(0)
		for i, c := range b {
			if i > 0 && c == last {
				if run++; run >= 64 {
					t.Fatalf("%s has a %d-byte run of 0x%02x", rel, run, c)
				}
			} else {
				run = 1
			}
			last = c
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if checked < 4 {
		t.Fatalf("only inspected %d files on the volume; the test is not exercising anything", checked)
	}
}

// A crash at any step of the commit protocol must leave the last *complete*
// generation intact and restorable.
func TestCrashMatrixLeavesTheLastCompleteGeneration(t *testing.T) {
	points := []failPoint{
		failAfterSomeBlobs,
		failAfterAllBlobs,
		failBeforeManifest,
		failAfterManifest,
		failBeforeCurrent,
	}
	for _, fp := range points {
		t.Run(string(fp), func(t *testing.T) {
			h := newHarness(t)
			h.write("storage/a.pdf", "%PDF first generation")
			h.write("storage/b.pdf", "%PDF also first")
			if err := h.v.Flush("baseline"); err != nil {
				t.Fatalf("baseline flush: %v", err)
			}
			good := h.tree()
			goodGen := h.v.Generation()

			// Now change the tree and crash partway through committing it.
			h.write("storage/c.pdf", "%PDF second generation")
			h.write("storage/a.pdf", "%PDF rewritten")
			if err := h.v.flush("crash", fp); err == nil {
				t.Fatalf("injected failure at %s did not abort the flush", fp)
			}

			h.reopen()

			gotGen := h.v.Generation()
			got := h.tree()
			if gotGen != goodGen {
				t.Fatalf("generation moved %d -> %d despite the crash", goodGen, gotGen)
			}
			if len(got) != len(good) {
				t.Fatalf("restored %d files, want the %d from the last complete generation", len(got), len(good))
			}
			for path, sum := range good {
				if got[path] != sum {
					t.Fatalf("%s does not match the last complete generation", path)
				}
			}

			// And the vault must still be usable afterwards.
			if err := h.v.Flush("after recovery"); err != nil {
				t.Fatalf("flush after recovery: %v", err)
			}
			if h.v.Generation() != goodGen+1 {
				t.Fatalf("generation after recovery = %d, want %d", h.v.Generation(), goodGen+1)
			}
		})
	}
}

// failAfterCurrent lands after the commit point, so the new generation must be
// the one that survives.
func TestCrashAfterCurrentKeepsTheNewGeneration(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a.pdf", "%PDF one")
	if err := h.v.Flush("baseline"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	h.write("storage/b.pdf", "%PDF two")
	if err := h.v.flush("crash", failAfterCurrent); err == nil {
		t.Fatal("injected failure did not abort")
	}

	h.reopen()
	if h.v.Generation() != 2 {
		t.Fatalf("generation = %d, want 2 (CURRENT had already advanced)", h.v.Generation())
	}
	if _, err := os.Stat(filepath.Join(h.workDir, "storage/b.pdf")); err != nil {
		t.Fatalf("the committed generation is missing b.pdf: %v", err)
	}
}

// A steady-state flush must not re-encrypt files that did not change; this is
// what keeps the flush interval independent of archive size.
func TestFlushIsIncremental(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		h.write(fmt.Sprintf("storage/doc%d.pdf", i), fmt.Sprintf("%%PDF document number %d", i))
	}
	if err := h.v.Flush("first"); err != nil {
		t.Fatalf("first flush: %v", err)
	}
	firstBlobs := h.blobCount()

	// A no-op flush: only the database snapshot should produce new blobs, and
	// the fake snapshotter writes identical bytes, so even those dedup.
	if err := h.v.Flush("noop"); err != nil {
		t.Fatalf("noop flush: %v", err)
	}
	if got := h.blobCount(); got != firstBlobs {
		t.Fatalf("a no-op flush wrote %d new blobs", got-firstBlobs)
	}

	h.write("storage/doc9.pdf", "%PDF brand new")
	if err := h.v.Flush("one new file"); err != nil {
		t.Fatalf("third flush: %v", err)
	}
	if got := h.blobCount(); got != firstBlobs+1 {
		t.Fatalf("adding one file produced %d new blobs, want 1", got-firstBlobs)
	}
}

// Identical content stored twice must occupy one blob.
func TestIdenticalContentIsDeduplicated(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a/same.pdf", "%PDF identical bytes")
	h.write("storage/b/same.pdf", "%PDF identical bytes")
	h.write("storage/c/other.pdf", "%PDF different")
	if err := h.v.Flush("dedup"); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// two distinct documents + two distinct database snapshots
	if got := h.blobCount(); got != 4 {
		t.Fatalf("blob count = %d, want 4 (duplicate content should share a blob)", got)
	}

	h.reopen()
	if h.read("storage/a/same.pdf") != h.read("storage/b/same.pdf") {
		t.Fatal("deduplicated files restored differently")
	}
}

func TestDerivedAndScratchPathsAreNotPersisted(t *testing.T) {
	h := newHarness(t)
	h.write("storage/keep.pdf", "%PDF keep me")
	h.write("bleve/documents/store/segment.zap", "plaintext ocr shadow")
	h.write("temp/split_upload/staged.pdf", "%PDF staged")
	h.write("ostmp/lemmary-doc-123", "%PDF ocr scratch copy")
	h.write("backups/pb_backup.zip", "PK\x03\x04 plaintext backup")
	h.write("data.db-wal", "write ahead log")
	h.write(".pb_temp_to_delete/junk", "junk")

	if err := h.v.Flush("exclusions"); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	v := h.v
	v.mu.Lock()
	m := v.prev
	v.mu.Unlock()

	var paths []string
	for _, e := range m.Entries {
		paths = append(paths, e.Path)
	}
	sort.Strings(paths)
	want := []string{"auxiliary.db", "data.db", "storage/keep.pdf"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("manifest holds %v, want %v", paths, want)
	}

	h.reopen()
	for _, gone := range []string{"bleve/documents/store/segment.zap", "ostmp/lemmary-doc-123", "backups/pb_backup.zip"} {
		if _, err := os.Stat(filepath.Join(h.workDir, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the round trip", gone)
		}
	}
}

// Flushing a working directory the vault never populated would destroy the
// archive, so it is refused outright.
func TestFlushRefusedBeforeLoad(t *testing.T) {
	root := t.TempDir()
	v, err := New(Options{Dir: filepath.Join(root, "vault"), WorkDir: filepath.Join(root, "work"), Enabled: true, AllowDiskWorkDir: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.releaseLock()

	if err := v.Flush("premature"); !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("got %v, want ErrNotLoaded", err)
	}
}

// A flush that would drop most of the archive is far more likely to be a bug
// than a genuine deletion.
func TestShrinkGuard(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		h.write(fmt.Sprintf("storage/doc%d.pdf", i), fmt.Sprintf("%%PDF %d", i))
	}
	if err := h.v.Flush("baseline"); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	baseGen := h.v.Generation()

	// Removing 6 of 12 entries leaves exactly half, which is allowed.
	for i := 0; i < 4; i++ {
		os.Remove(filepath.Join(h.workDir, fmt.Sprintf("storage/doc%d.pdf", i)))
	}
	if err := h.v.Flush("half"); err != nil {
		t.Fatalf("dropping to half should be allowed: %v", err)
	}

	// Dropping below half must be refused, and must not advance the generation.
	gen := h.v.Generation()
	for i := 4; i < 10; i++ {
		os.Remove(filepath.Join(h.workDir, fmt.Sprintf("storage/doc%d.pdf", i)))
	}
	err := h.v.Flush("mass deletion")
	if err == nil {
		t.Fatal("a flush dropping most of the archive was allowed")
	}
	if !strings.Contains(err.Error(), "AllowShrink") {
		t.Fatalf("the error should say how to override it: %v", err)
	}
	if h.v.Generation() != gen {
		t.Fatal("the refused flush advanced the generation")
	}
	if h.v.Generation() <= baseGen-1 {
		t.Fatal("unexpected generation")
	}

	// With the override it goes through.
	h.v.opts.AllowShrink = true
	if err := h.v.Flush("intended"); err != nil {
		t.Fatalf("AllowShrink flush: %v", err)
	}
}

// An unreadable newest manifest costs one flush interval, not the archive.
func TestCorruptNewestManifestFallsBackToThePrevious(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a.pdf", "%PDF generation one")
	if err := h.v.Flush("one"); err != nil {
		t.Fatalf("flush one: %v", err)
	}
	gen1 := h.tree()

	h.write("storage/b.pdf", "%PDF generation two")
	if err := h.v.Flush("two"); err != nil {
		t.Fatalf("flush two: %v", err)
	}
	if h.v.Generation() != 2 {
		t.Fatalf("generation = %d, want 2", h.v.Generation())
	}

	// Corrupt the newest manifest the way a bad sector would.
	p := manifestPath(h.dir, 2)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	b[len(b)-1] ^= 0xFF
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	h.reopen()

	if h.v.Generation() != 1 {
		t.Fatalf("fell back to generation %d, want 1", h.v.Generation())
	}
	got := h.tree()
	for path, sum := range gen1 {
		if got[path] != sum {
			t.Fatalf("%s does not match generation 1 after fallback", path)
		}
	}
	var warned bool
	for _, l := range h.logs {
		if strings.Contains(l, "unreadable") {
			warned = true
		}
	}
	if !warned {
		t.Fatal("the fallback was silent; it must be logged loudly")
	}
}

// Rolling back must stay possible, so blobs referenced by any retained
// generation survive garbage collection.
func TestGarbageCollectionKeepsRetainedGenerations(t *testing.T) {
	h := newHarness(t)
	h.v.opts.KeepGenerations = 2

	h.write("storage/doc.pdf", "%PDF version one")
	if err := h.v.Flush("v1"); err != nil {
		t.Fatalf("v1: %v", err)
	}
	h.write("storage/doc.pdf", "%PDF version two")
	if err := h.v.Flush("v2"); err != nil {
		t.Fatalf("v2: %v", err)
	}

	// Both versions are still reachable through a retained generation.
	m1, err := readManifest(h.dir, 1, h.v.mkey)
	if err != nil {
		t.Fatalf("read generation 1: %v", err)
	}
	for _, e := range m1.Entries {
		id, err := e.blobID()
		if err != nil {
			t.Fatalf("blobID: %v", err)
		}
		if !h.v.store.has(id) {
			t.Fatalf("blob for %s in retained generation 1 was collected", e.Path)
		}
	}

	// A third flush pushes generation 1 out of the window; its unique blob goes.
	oldBlob := ""
	for _, e := range m1.Entries {
		if e.Path == "storage/doc.pdf" {
			oldBlob = e.Blob
		}
	}
	h.write("storage/doc.pdf", "%PDF version three")
	if err := h.v.Flush("v3"); err != nil {
		t.Fatalf("v3: %v", err)
	}
	raw, _ := hex.DecodeString(oldBlob)
	var id StreamID
	copy(id[:], raw)
	if h.v.store.has(id) {
		t.Fatal("a blob from an evicted generation was not collected")
	}
	if gens, _ := listGenerations(h.dir); len(gens) != 2 {
		t.Fatalf("%d manifests retained, want 2", len(gens))
	}
}

func TestVerifyDetectsBlobCorruption(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a.pdf", "%PDF verify me")
	if err := h.v.Flush("verify"); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	n, err := h.v.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if n == 0 {
		t.Fatal("Verify checked nothing")
	}

	var target string
	_ = filepath.Walk(filepath.Join(h.dir, blobsDirName), func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() && target == "" {
			target = p
		}
		return nil
	})
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	b[len(b)/2] ^= 0xFF
	if err := os.WriteFile(target, b, 0o600); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	if _, err := h.v.Verify(); err == nil {
		t.Fatal("Verify passed over a corrupted blob")
	}
}

// Two processes sharing one vault would silently overwrite each other.
func TestVaultLockIsExclusive(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "vault")

	first, err := New(Options{Dir: dir, WorkDir: filepath.Join(root, "w1"), Enabled: true, AllowDiskWorkDir: true})
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	defer first.releaseLock()

	if _, err := New(Options{Dir: dir, WorkDir: filepath.Join(root, "w2"), Enabled: true, AllowDiskWorkDir: true}); err == nil {
		t.Fatal("a second process opened the same vault")
	} else if !strings.Contains(err.Error(), "another process") {
		t.Fatalf("the error should explain the conflict: %v", err)
	}

	first.releaseLock()
	second, err := New(Options{Dir: dir, WorkDir: filepath.Join(root, "w2"), Enabled: true, AllowDiskWorkDir: true})
	if err != nil {
		t.Fatalf("reopening after release: %v", err)
	}
	second.releaseLock()
}

// A disabled vault must be inert, so the application runs exactly as it does
// today.
func TestDisabledVaultIsInert(t *testing.T) {
	v, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if v.Enabled() {
		t.Fatal("a zero-valued vault reports enabled")
	}
	if err := v.Flush("noop"); err != nil {
		t.Fatalf("Flush on a disabled vault: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Fatalf("Close on a disabled vault: %v", err)
	}
	if v.MarkDirty() != 0 {
		t.Fatal("a disabled vault counted a dirty event")
	}
	if s := v.Stats(); s.Enabled {
		t.Fatal("stats report enabled")
	}
}

func TestInitAndUnlockWithRealCredential(t *testing.T) {
	root := t.TempDir()
	dir, work := filepath.Join(root, "vault"), filepath.Join(root, "work")

	v, err := New(Options{Dir: dir, WorkDir: work, Enabled: true, AllowDiskWorkDir: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if v.Initialized() {
		t.Fatal("a fresh directory reports initialised")
	}
	code, err := v.Init("user1", "s3cret-passphrase")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if code == "" {
		t.Fatal("Init returned no recovery code")
	}

	if err := os.WriteFile(filepath.Join(work, "storage-file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := v.Flush("init"); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	v.releaseLock()
	os.RemoveAll(work)

	// Wrong credential must fail without touching anything.
	v2, err := New(Options{Dir: dir, WorkDir: work, Enabled: true, AllowDiskWorkDir: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := v2.Unlock(Credential{Password: "wrong"}); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong password: got %v, want ErrWrongKey", err)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatal("a failed unlock created the working directory")
	}

	if err := v2.Unlock(Credential{Password: "s3cret-passphrase"}); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(work, "storage-file.txt"))
	if err != nil || string(b) != "hello" {
		t.Fatalf("content after unlock = %q, %v", b, err)
	}

	// And the recovery code is a genuine second way in.
	v2.releaseLock()
	os.RemoveAll(work)
	v3, err := New(Options{Dir: dir, WorkDir: work, Enabled: true, AllowDiskWorkDir: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v3.releaseLock()
	if err := v3.Unlock(Credential{RecoveryCode: code}); err != nil {
		t.Fatalf("unlock with recovery code: %v", err)
	}
}

// Regression: a rewrite that keeps the file length must still be captured.
//
// The unchanged-file cache keys on size and mtime, so two writes of equal length
// that land on the same filesystem timestamp are indistinguishable and the flush
// would go on persisting the stale blob — the working copy saying one thing and
// the vault holding another, with no error anywhere. This reproduced on an
// ordinary rewrite before the racily-clean guard existed.
//
// The collision is forced with Chtimes rather than left to timing, so the test
// fails deterministically if the guard is ever removed, on any filesystem
// granularity.
func TestSameSizeRewriteOnAStaleTimestampIsCaptured(t *testing.T) {
	h := newHarness(t)

	const before = "%PDF version one"
	const after = "%PDF version two"
	if len(before) != len(after) {
		t.Fatal("the test needs two bodies of identical length")
	}

	h.write("storage/doc.pdf", before)
	if err := h.v.Flush("first"); err != nil {
		t.Fatalf("first flush: %v", err)
	}

	// What the first generation recorded for this file.
	h.v.mu.Lock()
	var recorded int64
	for _, e := range h.v.prev.Entries {
		if e.Path == "storage/doc.pdf" {
			recorded = e.MTime
		}
	}
	h.v.mu.Unlock()
	if recorded == 0 {
		t.Fatal("the first generation did not record the document")
	}

	// Rewrite the contents but present the timestamp the manifest already has,
	// which is exactly what a same-tick rewrite looks like.
	h.write("storage/doc.pdf", after)
	stamp := time.Unix(0, recorded)
	if err := os.Chtimes(filepath.Join(h.workDir, "storage/doc.pdf"), stamp, stamp); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if err := h.v.Flush("second"); err != nil {
		t.Fatalf("second flush: %v", err)
	}

	h.reopen()

	if got := h.read("storage/doc.pdf"); got != after {
		t.Fatalf("restored %q, want %q — the flush reused a stale blob", got, after)
	}
}

// The cache still has to work, or a steady-state flush would re-read the whole
// archive. Files whose mtime is comfortably older than the last capture are
// taken on trust.
func TestUnchangedFilesUseTheCache(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 4; i++ {
		h.write(fmt.Sprintf("storage/doc%d.pdf", i), fmt.Sprintf("%%PDF body %d", i))
	}
	if err := h.v.Flush("first"); err != nil {
		t.Fatalf("first flush: %v", err)
	}

	// Backdate well beyond the granularity guard, as real files would be by the
	// time of the next flush.
	old := time.Now().Add(-time.Hour)
	for i := 0; i < 4; i++ {
		p := filepath.Join(h.workDir, fmt.Sprintf("storage/doc%d.pdf", i))
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}
	// The manifest still holds the pre-backdating mtimes, so capture a
	// generation that records the new ones before testing the cache.
	if err := h.v.Flush("record backdated mtimes"); err != nil {
		t.Fatalf("second flush: %v", err)
	}

	h.logs = nil
	if err := h.v.Flush("cached"); err != nil {
		t.Fatalf("third flush: %v", err)
	}

	var line string
	for _, l := range h.logs {
		if strings.Contains(l, "flushed (cached)") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no flush log line found in %v", h.logs)
	}
	if !strings.Contains(line, "reused=4") {
		t.Fatalf("expected the four backdated files to be reused, got: %s", line)
	}
}

// Decrypting into ordinary storage would write every document to the very disk
// the vault exists to protect, while every other guarantee still appeared to
// hold. That has to fail loudly rather than warn.
func TestWorkDirMustBeMemoryBacked(t *testing.T) {
	root := diskBackedTempDir(t)
	vaultDir := filepath.Join(root, "vault")

	// The refusal has to come from New, before anything is written: failing
	// later would already have saved a keyring, and the volume would boot as
	// "initialised" demanding a password nobody meant to set.
	_, err := New(Options{Dir: vaultDir, WorkDir: filepath.Join(root, "work"), Enabled: true})
	if err == nil {
		t.Fatal("opening a vault with a disk-backed working directory was allowed")
	}
	for _, want := range []string{"memory-backed", EnvAllowDiskWorkDir, "tmpfs"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error must explain the fix; %q is missing from: %v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(vaultDir, keyringName)); !os.IsNotExist(statErr) {
		t.Fatal("the refused open left a keyring behind, stranding the volume")
	}

	// A memory-backed directory must be accepted, or the check is just a
	// blanket refusal rather than a real test of the filesystem type.
	mem := memoryBackedTempDir(t)
	v2, err := New(Options{Dir: filepath.Join(root, "vault2"), WorkDir: mem, Enabled: true})
	if err != nil {
		t.Fatalf("New on tmpfs: %v", err)
	}
	defer v2.releaseLock()
	if _, err := v2.Init("u1", "password-for-this-test"); err != nil {
		t.Fatalf("a tmpfs working directory was rejected: %v", err)
	}
}

// diskBackedTempDir returns a directory that is definitely not memory-backed.
//
// It cannot just use t.TempDir(): on this machine /tmp is itself a tmpfs, which
// would make the negative case pass for the wrong reason. The package source
// directory is on ordinary storage.
func diskBackedTempDir(t *testing.T) string {
	t.Helper()
	for _, base := range []string{"", "."} {
		dir, err := os.MkdirTemp(base, "vault-disk-*")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		mem, err := isMemoryBacked(dir)
		if err != nil {
			t.Skipf("filesystem type is not detectable here: %v", err)
		}
		if !mem {
			return dir
		}
	}
	t.Skip("no disk-backed filesystem available to test against")
	return ""
}

// memoryBackedTempDir returns a tmpfs directory, skipping if none is available.
func memoryBackedTempDir(t *testing.T) string {
	t.Helper()
	for _, base := range []string{"/dev/shm", ""} {
		if base != "" {
			if _, err := os.Stat(base); err != nil {
				continue
			}
		}
		dir, err := os.MkdirTemp(base, "vault-mem-*")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		if mem, err := isMemoryBacked(dir); err == nil && mem {
			return dir
		}
	}
	t.Skip("no memory-backed filesystem available to test against")
	return ""
}

// A clean shutdown must not leave the decrypted archive behind.
func TestCloseWipesThePlaintextWorkingDirectory(t *testing.T) {
	h := newHarness(t)
	h.write("storage/secret.pdf", "%PDF sensitive")
	if err := h.v.Flush("before close"); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.workDir, "storage/secret.pdf")); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := h.v.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(h.workDir); !os.IsNotExist(err) {
		t.Fatalf("the plaintext working directory survived Close: %v", err)
	}

	// And the archive is still intact afterwards.
	h.open()
	if err := h.v.adopt(h.mk); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := h.read("storage/secret.pdf"); got != "%PDF sensitive" {
		t.Fatalf("content after wipe and reopen = %q", got)
	}
}

// A failed final flush means the working directory holds data the vault does
// not, so it must be kept rather than wiped.
func TestCloseKeepsTheWorkingDirectoryWhenTheFinalFlushFails(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a.pdf", "%PDF one")
	if err := h.v.Flush("baseline"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// A mass deletion trips the shrink guard, so the closing flush fails.
	if err := os.Remove(filepath.Join(h.workDir, "storage/a.pdf")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	h.write("storage/b.pdf", "%PDF unflushed and precious")
	h.v.mu.Lock()
	h.v.prev.Entries = append(h.v.prev.Entries, Entry{Path: "x1"}, Entry{Path: "x2"},
		Entry{Path: "x3"}, Entry{Path: "x4"}, Entry{Path: "x5"}, Entry{Path: "x6"})
	h.v.mu.Unlock()

	if err := h.v.Close(); err == nil {
		t.Fatal("Close reported success despite a failing flush")
	}
	if _, err := os.Stat(filepath.Join(h.workDir, "storage/b.pdf")); err != nil {
		t.Fatalf("the unflushed file was wiped along with the working directory: %v", err)
	}
}

// A keyring on disk is what marks an instance initialised, so one must never be
// written for a vault that then fails to materialise: the next boot would demand
// a password nobody deliberately set, against an otherwise empty volume.
func TestFailedInitDoesNotStrandTheVolume(t *testing.T) {
	root := t.TempDir()
	vaultDir := filepath.Join(root, "vault")

	// A working directory that cannot be created: adopt fails after the keyring
	// has been built but before it should be persisted.
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	v, err := New(Options{
		Dir: vaultDir, WorkDir: filepath.Join(blocker, "work"),
		Enabled: true, AllowDiskWorkDir: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.releaseLock()

	if _, err := v.Init("u1", "password-for-this-test"); err == nil {
		t.Fatal("Init succeeded despite an unusable working directory")
	}
	if _, err := os.Stat(filepath.Join(vaultDir, keyringName)); !os.IsNotExist(err) {
		t.Fatal("a keyring was persisted for a vault that failed to initialise")
	}
	if v.Initialized() {
		t.Fatal("the vault reports initialised after a failed Init")
	}
}

// A flush that runs in a process which never opened the databases must not
// commit, or the next unlock restores documents with no metadata at all.
//
// This is reachable without anything going wrong: the snapshotter is installed
// from OnBootstrap, PocketBase skips bootstrap entirely for --help, --version
// and any unknown command, and OnTerminate still fires for all of them. The
// shrink guard does not catch it — dropping two database entries out of many is
// nowhere near halving the archive — so the first flush would silently replace a
// good generation with a metadata-free one.
func TestFlushRefusesWithoutASnapshotterOnceDatabasesExist(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a/one.pdf", "first document")
	h.write("storage/a/two.pdf", "second document")
	h.write("storage/a/three.pdf", "third document")
	if err := h.v.Flush("first"); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	good := h.v.Generation()

	// A process that never bootstrapped: unlocked and restored, but with no
	// snapshotter installed.
	h.v.SetSnapshotter(nil)
	err := h.v.Flush("terminate")
	if err == nil {
		t.Fatal("a flush with no snapshotter committed over a generation that had databases")
	}
	if !strings.Contains(err.Error(), "without a database snapshot") {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.v.Generation() != good {
		t.Fatalf("generation moved to %d despite the refusal, want %d", h.v.Generation(), good)
	}

	// And the archive still restores with its databases.
	h.reopen()
	if got := h.read("data.db"); !strings.Contains(got, "pretend database") {
		t.Fatalf("data.db did not survive: %q", got)
	}
}

// A fresh vault has no databases to lose, so the guard must not block the first
// flush of one that genuinely has none.
func TestFlushWithoutASnapshotterIsFineWhenNothingHadDatabases(t *testing.T) {
	h := newHarness(t)
	h.v.SetSnapshotter(nil)
	h.write("storage/a/one.pdf", "only a document")
	if err := h.v.Flush("first"); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if h.v.Generation() == 0 {
		t.Fatal("nothing was committed")
	}
}

// Nesting the vault inside the working directory is unrecoverable: the working
// directory is emptied on every unlock, so the wipe would delete the keyring,
// every manifest and every blob at the one moment the master key existed only in
// memory. Nothing downstream would notice — the restore succeeds against an
// empty directory and the first flush commits that emptiness.
func TestNewRefusesNestedDirectories(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name    string
		dir     string
		workDir string
	}{
		{"vault inside the working directory", filepath.Join(root, "data", "vault"), filepath.Join(root, "data")},
		{"working directory inside the vault", filepath.Join(root, "data"), filepath.Join(root, "data", "work")},
		{"the same directory", filepath.Join(root, "data"), filepath.Join(root, "data")},
		{"the same directory spelled differently", filepath.Join(root, "data"), filepath.Join(root, "data") + string(filepath.Separator) + "."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := New(Options{Dir: tc.dir, WorkDir: tc.workDir, Enabled: true, AllowDiskWorkDir: true})
			if err == nil {
				v.releaseLock()
				t.Fatal("a nested configuration was accepted")
			}
			if !strings.Contains(err.Error(), "separate trees") && !strings.Contains(err.Error(), "about to open") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// Siblings are the intended layout and must keep working.
func TestNewAcceptsSiblingDirectories(t *testing.T) {
	root := t.TempDir()
	v, err := New(Options{
		Dir: filepath.Join(root, "vault"), WorkDir: filepath.Join(root, "work"),
		Enabled: true, AllowDiskWorkDir: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v.releaseLock()
}

// put hashes a file and then re-reads it to seal. A file rewritten between the
// two passes would otherwise be stored under the content address of bytes it
// does not hold — and because the AEAD authenticates the blob against its id,
// and the blob is internally consistent, nothing downstream could ever detect
// it. The damage lands much later: some unrelated file whose content genuinely
// hashes to that address is uploaded, dedupe reuses the blob, and that document
// silently restores holding the wrong bytes.
func TestPutDetectsAFileRewrittenBetweenTheHashAndTheSeal(t *testing.T) {
	h := newHarness(t)
	store := h.v.store

	path := filepath.Join(h.workDir, "racy.bin")
	original := bytes.Repeat([]byte("A"), 4096)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	sum := sha256.Sum256(original)
	staleID := store.blobID(sum[:])

	// Rewrite the file every time it is opened, so both passes of every attempt
	// see different content and the retry budget is exhausted.
	rewritten := 0
	swap := func() {
		rewritten++
		body := bytes.Repeat([]byte{byte('B' + rewritten)}, 4096)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("rewrite: %v", err)
		}
	}
	orig := hookAfterHash
	hookAfterHash = swap
	defer func() { hookAfterHash = orig }()

	if _, _, err := store.put(path); err == nil {
		t.Fatal("put accepted a file that was rewritten under it")
	}
	// Above all: no blob may be left behind under an address it does not match.
	if store.has(staleID) {
		t.Fatal("a blob was left stored under the content address of bytes it does not hold")
	}
}

// The ordinary case must not pay for the check above.
func TestPutStoresAStableFileInOnePass(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.workDir, "stable.bin")
	if err := os.WriteFile(path, []byte("unchanging"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	id, written, err := h.v.store.put(path)
	if err != nil || !written {
		t.Fatalf("put = %v, written=%v", err, written)
	}
	if err := h.v.store.verify(id); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// Wipe must report success when the working directory is a mount point. In the
// intended deployment it is a tmpfs mount, unlinking a mount point always fails
// with EBUSY, and returning that would print an alarming failure to remove the
// plaintext on every clean shutdown — at the exact moment an operator most needs
// to trust the message — while the plaintext had in fact been removed.
func TestWipeEmptiesTheDirectoryAndToleratesAnUnremovableRoot(t *testing.T) {
	h := newHarness(t)
	h.write("storage/a/one.pdf", "plaintext that must not survive")
	h.write("data.db", "plaintext metadata")

	if err := h.v.Wipe(); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	// An ordinary directory is removed outright; a mount point survives but is
	// emptied. Either way no plaintext may remain, and neither is an error.
	entries, err := os.ReadDir(h.workDir)
	if err == nil && len(entries) != 0 {
		t.Fatalf("Wipe left %d entries behind", len(entries))
	}
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read the working directory: %v", err)
	}

	// The part that must not be reported as a failure: a root that cannot be
	// unlinked, which is what a tmpfs mount point is on every clean shutdown.
	blocked := filepath.Join(t.TempDir(), "mountpoint")
	if err := os.MkdirAll(filepath.Join(blocked, "sub"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "sub", "doc.pdf"), []byte("plaintext"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	stub := &Vault{opts: Options{Enabled: true, WorkDir: blocked, Log: func(string, ...any) {}}}
	// Make the root itself unremovable the way a mount point is, by taking away
	// write permission on its parent.
	parent := filepath.Dir(blocked)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(parent, 0o700)

	if err := stub.Wipe(); err != nil {
		t.Fatalf("Wipe reported a failure for an unremovable root: %v", err)
	}
	left, err := os.ReadDir(blocked)
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("the plaintext was not removed: %d entries remain", len(left))
	}
}

// Not being able to run the check is not permission to skip it.
//
// The filesystem-type test is Linux-only, and a statfs can refuse. Treating
// either as "carry on" reaches exactly the outcome TestWorkDirMustBeMemoryBacked
// refuses — the archive decrypted onto ordinary storage with every other
// guarantee still appearing to hold — only silently, and on every boot.
func TestWorkDirRefusesWhenMemoryBackingCannotBeChecked(t *testing.T) {
	root := t.TempDir()
	vaultDir := filepath.Join(root, "vault")

	original := checkMemoryBacked
	checkMemoryBacked = func(string) (bool, error) {
		return false, errors.New("statfs is not implemented on this platform")
	}
	t.Cleanup(func() { checkMemoryBacked = original })

	_, err := New(Options{Dir: vaultDir, WorkDir: filepath.Join(root, "work"), Enabled: true})
	if err == nil {
		t.Fatal("opening a vault whose working directory could not be checked was allowed")
	}
	for _, want := range []string{"cannot verify", EnvAllowDiskWorkDir} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error must explain the fix; %q is missing from: %v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(vaultDir, keyringName)); !os.IsNotExist(statErr) {
		t.Fatal("the refused open left a keyring behind, stranding the volume")
	}

	// And the escape hatch still works: an operator who knows their platform
	// says so, and the vault opens.
	v, err := New(Options{
		Dir: vaultDir, WorkDir: filepath.Join(root, "work"),
		Enabled: true, AllowDiskWorkDir: true,
	})
	if err != nil {
		t.Fatalf("%s did not accept an uncheckable working directory: %v", EnvAllowDiskWorkDir, err)
	}
	t.Cleanup(func() { _ = v.Close() })
}
