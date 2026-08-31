// Package vault keeps a PocketBase data directory encrypted on the persistent
// volume and plaintext only in memory.
//
// The shape of the thing: the volume holds a keyring, a chain of sealed
// manifests, and a content-addressed store of sealed blobs. Nothing else. On
// unlock, a credential unwraps the master key, the newest manifest is
// materialised into a memory-backed working directory, and PocketBase is pointed
// at that directory. From then on the application is an ordinary single-tenant
// install that happens to live in RAM; it needs no knowledge of any of this,
// which is the entire point — the alternative, sealing individual database
// columns, means touching every query path and still leaves the uploaded files
// and the search index in the clear.
//
// What it protects: the volume is ciphertext whenever the process is stopped,
// and from boot until the first sign-in. A stolen disk, a leaked snapshot, or an
// operator browsing the filesystem gets nothing.
//
// What it does not protect: anything, from someone who controls the running
// process. The key is in memory once unlocked, and whoever runs the binary can
// patch it to capture the key at unlock. This is at-rest encryption, not
// zero-knowledge, and it must not be described as the latter.
package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lemmary/backend/internal/crypt"
)

// Reserved names inside the working directory.
const (
	stageDirName = ".vault_stage"
	osTempName   = "ostmp"
	blobsDirName = "blobs"
)

// excludedPrefixes are working-directory paths the vault deliberately does not
// persist.
//
//   - bleve is derived data: the index self-heals and is rebuilt from the
//     database on boot, and it is also a full plaintext shadow of every
//     document's OCR text, so keeping it out of the vault removes a whole class
//     of leak rather than merely encrypting it.
//   - temp holds staged uploads whose registry is in-memory anyway, so a restart
//     already orphans them.
//   - the rest are PocketBase scratch, our own staging, and the OS temp
//     directory we redirect into RAM.
var excludedPrefixes = []string{
	"bleve",
	"temp",
	"backups",
	"lost+found",
	".pb_temp_to_delete",
	".notify",
	stageDirName,
	osTempName,
}

// databaseFiles are snapshotted rather than copied, so the live files and their
// WAL sidecars are skipped during the walk.
var databaseFiles = []string{"data.db", "auxiliary.db"}

func isExcluded(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range excludedPrefixes {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	for _, db := range databaseFiles {
		// The snapshot supplies these; the live file and its -wal/-shm sidecars
		// are never captured directly because copying them mid-write is exactly
		// how you get a torn database.
		if rel == db || strings.HasPrefix(rel, db+"-") {
			return true
		}
	}
	return false
}

// Snapshotter produces consistent copies of the application databases into a
// staging directory.
//
// It is an interface so the storage engine can be tested without PocketBase; the
// real implementation uses VACUUM INTO.
type Snapshotter interface {
	SnapshotDatabases(stageDir string) error
}

// Logger is the minimal logging surface the vault needs.
type Logger func(format string, args ...any)

// Options configures a vault.
type Options struct {
	// Dir is the persistent vault directory (the Docker volume).
	Dir string
	// WorkDir is the memory-backed directory the plaintext lives in.
	WorkDir string
	// Enabled reports whether encryption is on at all. When false every method
	// is a no-op and the application runs exactly as it does today.
	Enabled bool
	// KeepGenerations bounds rollback depth; zero means the default.
	KeepGenerations int
	// AllowShrink disables the guard that refuses a flush which would drop more
	// than half the archive.
	AllowShrink bool
	// AllowDiskWorkDir permits a working directory that is not memory-backed.
	// Only tests and local development should set it.
	AllowDiskWorkDir bool
	// Log receives progress and warnings.
	Log Logger
}

func (o *Options) applyDefaults() {
	if o.KeepGenerations <= 0 {
		o.KeepGenerations = keepGenerations
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
}

// Vault is an opened, unlocked encrypted data directory.
type Vault struct {
	opts Options

	mu        sync.Mutex
	loaded    bool
	finalized bool
	prev      *Manifest

	kr    *Keyring
	mk    crypt.Key
	store *blobStore
	mkey  crypt.Key // manifest key

	lock *os.File

	flushMu sync.Mutex
	// gateMu serialises the unlock gate's check-then-initialise.
	gateMu sync.Mutex
	// keyringMu serialises keyring mutation and its save. The enrollment hooks
	// run on PocketBase's request goroutines, so two concurrent account saves
	// would otherwise race on the wrap list — and the loser's wrap could be
	// missing from the keyring that lands on disk, leaving that user unable to
	// unlock after the next restart.
	keyringMu sync.Mutex
	snap      Snapshotter

	dirty   atomicCounter
	flushes atomicCounter
}

// Enabled reports whether this vault does anything.
func (v *Vault) Enabled() bool { return v != nil && v.opts.Enabled }

// WorkDir is the plaintext directory PocketBase should use as its data dir.
func (v *Vault) WorkDir() string { return v.opts.WorkDir }

// Dir is the persistent vault directory.
func (v *Vault) Dir() string { return v.opts.Dir }

// Loaded reports whether the vault has been materialised. Nothing may be
// flushed before this is true: writing a manifest built from an empty working
// directory over a good vault is the one unrecoverable mistake this design can
// make, so it is gated here rather than at each call site.
func (v *Vault) Loaded() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.loaded
}

// Generation returns the currently committed generation, or zero.
func (v *Vault) Generation() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.prev == nil {
		return 0
	}
	return v.prev.Gen
}

// SetSnapshotter installs the database snapshot strategy.
func (v *Vault) SetSnapshotter(s Snapshotter) { v.snap = s }

// Keyring exposes the keyring for enrollment operations.
func (v *Vault) Keyring() *Keyring { return v.kr }

// MasterKey returns the unwrapped master key. Callers must not retain it.
func (v *Vault) MasterKey() crypt.Key { return v.mk }

// SaveKeyring persists keyring changes made through Keyring.
func (v *Vault) SaveKeyring() error { return v.kr.Save(v.opts.Dir) }

// UpdateKeyring runs fn with exclusive access to the keyring, then persists the
// result. Every mutation after the vault is serving must go through here: the
// enrollment hooks run on concurrent request goroutines, and an unserialised
// read-modify-write of the wrap list can persist a keyring missing the losing
// goroutine's wrap — a user who silently cannot unlock after the next restart.
//
// An error from fn skips the save and is returned unchanged, so a caller can
// treat conditions like ErrLastWrap as "leave the keyring alone".
func (v *Vault) UpdateKeyring(fn func(kr *Keyring) error) error {
	v.keyringMu.Lock()
	defer v.keyringMu.Unlock()
	if v.kr == nil {
		return ErrNoKeyring
	}
	if err := fn(v.kr); err != nil {
		return err
	}
	return v.kr.Save(v.opts.Dir)
}

// New prepares a vault without unlocking it.
func New(opts Options) (*Vault, error) {
	opts.applyDefaults()
	v := &Vault{opts: opts}
	if !opts.Enabled {
		return v, nil
	}
	if opts.Dir == "" || opts.WorkDir == "" {
		return nil, errors.New("vault: Dir and WorkDir are required when enabled")
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, err
	}
	// Before anything is created or read: refusing late would already have
	// written a keyring, leaving a half-initialised vault behind.
	v.opts = opts
	if err := v.checkWorkDirIsMemoryBacked(); err != nil {
		return nil, err
	}
	lock, err := acquireLock(opts.Dir)
	if err != nil {
		return nil, err
	}
	v.lock = lock

	kr, err := LoadKeyring(opts.Dir)
	if err != nil && !errors.Is(err, ErrNoKeyring) {
		v.releaseLock()
		return nil, err
	}
	v.kr = kr
	return v, nil
}

// Initialized reports whether this vault has ever been set up.
func (v *Vault) Initialized() bool { return v.kr != nil }

// Init creates a brand new vault around a first credential.
//
// It returns the recovery code, which is shown once and never stored in
// recoverable form.
func (v *Vault) Init(userID, password string) (string, error) {
	if v.kr != nil {
		return "", errors.New("vault: already initialised")
	}
	if err := v.checkNoPlaintextInstall(); err != nil {
		return "", err
	}
	kr, mk, code, err := NewKeyring(userID, password)
	if err != nil {
		return "", err
	}

	// Adopt first, save second. A keyring on disk is what makes an instance
	// "initialised", so writing one for a vault that then fails to materialise
	// would strand the volume: the next boot would demand a password nobody
	// meant to set.
	v.kr = kr
	if err := v.adopt(mk); err != nil {
		v.kr = nil
		return "", err
	}
	if err := kr.Save(v.opts.Dir); err != nil {
		v.kr = nil
		return "", err
	}
	v.opts.Log("vault: initialised, master key %s", crypt.KeyID(mk))
	return code, nil
}

// Unlock opens the vault with a credential and materialises the working
// directory.
func (v *Vault) Unlock(c Credential) error {
	if v.kr == nil {
		return ErrNoKeyring
	}
	mk, wrapID, err := v.kr.Unlock(c)
	if err != nil {
		return err
	}
	if err := v.adopt(mk); err != nil {
		return err
	}
	v.opts.Log("vault: unlocked via wrap %q at generation %d", wrapID, v.Generation())
	return nil
}

// adopt installs the master key, derives subkeys, and restores the working dir.
func (v *Vault) adopt(mk crypt.Key) error {
	blobKey, manifestKey, nameKey, err := v.kr.Subkeys(mk)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.mk = mk
	v.mkey = manifestKey
	v.store = &blobStore{
		dir:     filepath.Join(v.opts.Dir, blobsDirName),
		blobKey: blobKey,
		nameKey: nameKey,
	}
	v.mu.Unlock()

	if err := os.MkdirAll(v.store.dir, 0o700); err != nil {
		return err
	}
	return v.restore()
}

// checkNoPlaintextInstall refuses to create a vault on top of an existing
// unencrypted install.
//
// Without this the failure is quiet and awful: the vault directory defaults to
// pb_data, so switching encryption on for a running install would initialise an
// empty vault, restore an empty working directory, and present the operator with
// an archive that appears to have lost every document — while the real data sat
// beside it as plaintext files that nothing would ever clean up or encrypt.
//
// Migration is deliberately manual, because doing it properly means moving the
// data onto a fresh volume: deleting the old files would leave their contents
// recoverable in free space, which is not encryption at rest by any useful
// definition.
func (v *Vault) checkNoPlaintextInstall() error {
	var found []string
	for _, name := range append([]string{}, databaseFiles...) {
		if st, err := os.Stat(filepath.Join(v.opts.Dir, name)); err == nil && st.Mode().IsRegular() {
			found = append(found, name)
		}
	}
	storage := filepath.Join(v.opts.Dir, "storage")
	if entries, err := os.ReadDir(storage); err == nil && len(entries) > 0 {
		found = append(found, "storage/")
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf(
		"vault: %s already contains an unencrypted install (%s). Initialising here would start with an empty "+
			"archive and leave that data behind in the clear. Migrate deliberately instead: start the encrypted "+
			"instance against an empty %s on a NEW volume, re-import the documents, then destroy the old volume — "+
			"deleting the files in place would leave their contents recoverable from free space",
		v.opts.Dir, strings.Join(found, ", "), EnvDir)
}

// checkWorkDirIsMemoryBacked refuses to decrypt into ordinary storage.
//
// The entire promise here is that plaintext never reaches persistent disk, and
// nothing else in the design enforces it: point WorkDir at a normal directory
// and the archive is silently decrypted onto the very medium it was being
// protected from, with every other guarantee still appearing to hold. A
// misconfiguration that quietly voids the feature is worse than a failure to
// start, so this refuses rather than warns.
func (v *Vault) checkWorkDirIsMemoryBacked() error {
	if v.opts.AllowDiskWorkDir {
		return nil
	}
	if err := os.MkdirAll(v.opts.WorkDir, 0o700); err != nil {
		return err
	}
	mem, err := isMemoryBacked(v.opts.WorkDir)
	if err != nil {
		// Cannot tell (non-Linux, or statfs refused): say so and continue rather
		// than blocking a platform this check simply does not cover.
		v.opts.Log("vault: cannot verify that %s is memory-backed: %v", v.opts.WorkDir, err)
		return nil
	}
	if !mem {
		return fmt.Errorf(
			"vault: %s is not a memory-backed filesystem, so decrypting into it would write every document to disk in the clear. Mount a tmpfs there (in compose: tmpfs: [\"%s:size=2g,mode=0700\"]), or set %s=1 to accept plaintext on disk",
			v.opts.WorkDir, v.opts.WorkDir, EnvAllowDiskWorkDir)
	}
	return nil
}

// Finalize performs the last flush while the databases are still open.
//
// PocketBase triggers OnTerminate for every command, so this runs on a clean
// exit of any kind; Close then only has to wipe and unlock. Splitting the two
// matters because by the time a deferred Close runs the databases are closed
// and the snapshot would fail.
func (v *Vault) Finalize() {
	if !v.Enabled() || !v.Loaded() {
		return
	}
	v.mu.Lock()
	if v.finalized {
		v.mu.Unlock()
		return
	}
	v.mu.Unlock()

	err := v.Flush("terminate")

	v.mu.Lock()
	v.finalized = err == nil
	v.mu.Unlock()
	if err != nil {
		v.opts.Log("vault: the shutdown flush failed, so the working directory will be kept: %v", err)
	}
}

// Close flushes, wipes the plaintext working directory, and releases the lock.
//
// The wipe is not merely tidiness. On a correctly configured deployment the
// working directory is tmpfs and vanishes with the container anyway, but if it
// ever lands on real storage the decrypted archive would outlive the process and
// the whole feature would be silently defeated. Removing it on the way out means
// a clean shutdown always leaves ciphertext only.
func (v *Vault) Close() error {
	if !v.Enabled() {
		return nil
	}
	var flushErr error
	v.mu.Lock()
	done := v.finalized
	v.mu.Unlock()
	if v.Loaded() && !done {
		flushErr = v.Flush("close")
	}
	if flushErr == nil {
		if err := v.Wipe(); err != nil {
			v.opts.Log("vault: could not remove the plaintext working directory %s: %v", v.opts.WorkDir, err)
		}
	} else {
		// Never destroy the only copy of data that failed to reach the vault.
		v.opts.Log("vault: keeping the working directory %s because the final flush failed: %v", v.opts.WorkDir, flushErr)
	}
	v.releaseLock()
	return flushErr
}

func (v *Vault) releaseLock() {
	if v.lock != nil {
		releaseLock(v.lock)
		v.lock = nil
	}
}

// Wipe removes the plaintext working directory. It is called after a final
// flush, so that a stopped container leaves nothing behind.
func (v *Vault) Wipe() error {
	if v.opts.WorkDir == "" {
		return nil
	}
	return os.RemoveAll(v.opts.WorkDir)
}

// Stats describes the vault for a status endpoint.
type Stats struct {
	Enabled     bool   `json:"enabled"`
	Loaded      bool   `json:"loaded"`
	Generation  uint64 `json:"generation"`
	Entries     int    `json:"entries"`
	PlainBytes  int64  `json:"plain_bytes"`
	VaultBytes  int64  `json:"vault_bytes"`
	Flushes     int64  `json:"flushes"`
	PendingDirt int64  `json:"pending_dirty"`
	MasterKeyFP string `json:"master_key_fp,omitempty"`
}

// Stats reports current vault state.
func (v *Vault) Stats() Stats {
	s := Stats{Enabled: v.Enabled()}
	if !s.Enabled {
		return s
	}
	v.mu.Lock()
	s.Loaded = v.loaded
	if v.prev != nil {
		s.Generation = v.prev.Gen
		s.Entries = len(v.prev.Entries)
		s.PlainBytes = v.prev.TotalSize()
	}
	if v.kr != nil {
		s.MasterKeyFP = v.kr.MKFP
	}
	v.mu.Unlock()

	s.Flushes = v.flushes.get()
	s.PendingDirt = v.dirty.get()
	if n, err := dirSize(v.opts.Dir); err == nil {
		s.VaultBytes = n
	}
	return s
}

func nowUnixNano() int64 { return time.Now().UnixNano() }

// atomicCounter is a tiny mutex-free counter used for metrics.
type atomicCounter struct {
	mu sync.Mutex
	n  int64
}

func (c *atomicCounter) add(n int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += n
	return c.n
}

func (c *atomicCounter) get() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *atomicCounter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n = 0
}

// InstallTempDir points the process temporary directory inside the working
// directory.
//
// This is what stops the OCR and preview paths writing plaintext copies of
// documents onto real disk. There are seven such sites across the worker,
// preview, pdfsplit, pdftool, appapi and limits packages, all using
// os.CreateTemp/os.MkdirTemp with an empty dir argument, and in the container
// image /tmp is the writable overlay — actual disk. os.TempDir consults the
// environment on every call and exec'd children inherit it, so one assignment
// covers all of them, poppler included, with no change to any of those
// packages.
func (v *Vault) InstallTempDir() error {
	if !v.Enabled() {
		return nil
	}
	dir := filepath.Join(v.opts.WorkDir, osTempName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if err := os.Setenv(key, dir); err != nil {
			return err
		}
	}
	return nil
}

// GuardDataDirFlag refuses to start when --dir is passed alongside an enabled
// vault.
//
// PocketBase parses --dir eagerly and lets it override DefaultDataDir, so a
// stale entrypoint or a debugging session would silently produce a fully
// plaintext install on the persistent volume with nothing to indicate anything
// was wrong. Failing loudly is the only safe response.
func GuardDataDirFlag(args []string) error {
	for i, a := range args {
		if a == "--" {
			return nil
		}
		if a == "--dir" || a == "-dir" {
			if i+1 < len(args) {
				return dataDirFlagError(args[i+1])
			}
			return dataDirFlagError("")
		}
		if strings.HasPrefix(a, "--dir=") || strings.HasPrefix(a, "-dir=") {
			_, val, _ := strings.Cut(a, "=")
			return dataDirFlagError(val)
		}
	}
	return nil
}

func dataDirFlagError(val string) error {
	return fmt.Errorf(
		"--dir %s cannot be combined with %s=1: it would override the vault's in-memory data directory and write every document to disk in the clear. Set %s to choose where the encrypted vault lives instead",
		val, EnvEnabled, EnvDir)
}
