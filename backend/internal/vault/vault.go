// Package vault keeps a PocketBase data directory encrypted on the persistent
// volume and plaintext only in memory.
//
// The volume holds a keyring, a chain of sealed manifests, and a
// content-addressed store of sealed blobs. On unlock, a credential unwraps the
// master key, the newest manifest is materialised into a memory-backed working
// directory, and PocketBase is pointed at it. The application then needs no
// knowledge of any of this, which is the point: sealing individual database
// columns instead would touch every query path and still leave the uploaded
// files and the search index in the clear.
//
// The volume is ciphertext whenever the process is stopped, and from boot until
// the first sign-in. It protects nothing from someone who controls the running
// process, whose memory holds the key. This is at-rest encryption, not
// zero-knowledge, and must not be described as the latter.
package vault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lemmary/backend/internal/crypt"
	"lemmary/backend/internal/inflight"
)

// Reserved names inside the working directory.
const (
	stageDirName = ".vault_stage"
	osTempName   = "ostmp"
	blobsDirName = "blobs"
)

// excludedPrefixes are working-directory paths the vault deliberately does not
// persist. bleve is derived data that self-heals, and is also a full plaintext
// shadow of every document's OCR text, so leaving it out removes a class of
// leak rather than encrypting it; temp holds staged uploads a restart already
// orphans; the rest are scratch directories.
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
		// The snapshot supplies these: copying the live file and its -wal/-shm sidecars
		// mid-write is how a torn database happens.
		if rel == db || strings.HasPrefix(rel, db+"-") {
			return true
		}
	}
	return false
}

// Snapshotter is an interface so the storage engine can be tested without
// PocketBase; the real implementation uses VACUUM INTO.
type Snapshotter interface {
	SnapshotDatabases(stageDir string) error
}

type Logger func(format string, args ...any)

type Options struct {
	// Dir is the persistent vault directory (the Docker volume).
	Dir string
	// WorkDir is the memory-backed directory the plaintext lives in.
	WorkDir string
	// Enabled false makes every method a no-op.
	Enabled bool
	// KeepGenerations bounds rollback depth; zero means the default.
	KeepGenerations int
	// AllowShrink disables the guard that refuses a flush which would drop more
	// than half the archive.
	AllowShrink bool
	// AllowDiskWorkDir permits a working directory that is not memory-backed. Only
	// tests and local development should set it.
	AllowDiskWorkDir bool
	// AllowInsecureGate accepts serving the unlock form over cleartext HTTP on an
	// address something other than this host can reach.
	AllowInsecureGate bool
	Log               Logger
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
	// keyringMu serialises keyring mutation and its save: the enrollment hooks run
	// on request goroutines, so two concurrent account saves would race on the wrap
	// list and the loser's wrap could be missing from the keyring on disk.
	keyringMu sync.Mutex
	snap      Snapshotter

	dirty   atomicCounter
	flushes atomicCounter
	// Set by Register, so the consume folder knows what a flush has sealed.
	seal *inflight.Seal
}

func (v *Vault) Enabled() bool { return v != nil && v.opts.Enabled }

// WorkDir is the plaintext directory PocketBase uses as its data dir.
func (v *Vault) WorkDir() string { return v.opts.WorkDir }

func (v *Vault) Dir() string { return v.opts.Dir }

// Loaded gates every flush: writing a manifest built from an empty working
// directory over a good vault is the one unrecoverable mistake this design can
// make, so it is checked here rather than at each call site.
func (v *Vault) Loaded() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.loaded
}

func (v *Vault) Generation() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.prev == nil {
		return 0
	}
	return v.prev.Gen
}

// SetSnapshotter is called from the bootstrap hook, on a different goroutine
// from the flushes that read it, so it takes the lock like every other field.
func (v *Vault) SetSnapshotter(s Snapshotter) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.snap = s
}

func (v *Vault) Keyring() *Keyring { return v.kr }

// MasterKey returns the unwrapped master key. Callers must not retain it.
func (v *Vault) MasterKey() crypt.Key { return v.mk }

// UpdateKeyring is how every mutation after the vault is serving must go: the
// enrollment hooks run on concurrent request goroutines, and an unserialised
// read-modify-write of the wrap list can persist a keyring missing the losing
// goroutine's wrap, leaving that user unable to unlock after a restart.
//
// An error from fn skips the save and is returned unchanged, so a caller can
// treat ErrLastWrap as "leave the keyring alone".
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
	if err := checkDirsDisjoint(opts.Dir, opts.WorkDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, err
	}
	// Before anything is created or read: refusing late would already have written
	// a keyring, leaving a half-initialised vault behind.
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

func (v *Vault) Initialized() bool { return v.kr != nil }

// Init returns the recovery code, which is shown once and never stored in
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
	// initialised, so writing one for a vault that then fails to materialise would
	// strand the volume: the next boot would demand a password nobody meant to set.
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

// checkDirsDisjoint refuses a configuration where either directory contains the
// other. The working directory is emptied on every unlock: nest the vault
// inside it (VAULT_WORKDIR=/data with VAULT_DIR=/data/vault, natural enough to
// write) and that wipe deletes the keyring, every manifest and every blob at
// the one moment the master key exists only in memory. Nothing downstream would
// notice, and the first flush commits the emptiness.
//
// The reverse nesting is merely bad, plaintext inside the ciphertext-only
// directory, and is refused in the same breath.
//
// Comparison is on cleaned absolute paths; symlinks are not resolved because
// neither directory need exist yet, and a check that catches the obvious
// spelling beats one that cannot run until after the damage.
func checkDirsDisjoint(dir, workDir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absWork, err := filepath.Abs(workDir)
	if err != nil {
		return err
	}

	if absDir == absWork {
		return fmt.Errorf(
			"vault: %s and %s are both %s. The working directory is emptied on every unlock, so this would delete the vault it was about to open",
			EnvDir, EnvWorkDir, absDir)
	}
	if withinDir(absWork, absDir) {
		return fmt.Errorf(
			"vault: %s (%s) is inside %s (%s). The working directory is emptied on every unlock, which would delete the keyring, the manifests and every blob while the master key existed only in memory — the archive would be unrecoverable. Put them in separate trees",
			EnvDir, absDir, EnvWorkDir, absWork)
	}
	if withinDir(absDir, absWork) {
		return fmt.Errorf(
			"vault: %s (%s) is inside %s (%s), so the decrypted archive would be written into the directory that is supposed to hold only ciphertext. Put them in separate trees",
			EnvWorkDir, absWork, EnvDir, absDir)
	}
	return nil
}

// checkNoPlaintextInstall refuses to create a vault over an existing
// unencrypted install. The vault directory defaults to pb_data, so switching
// encryption on for a running install would initialise an empty vault and
// present an archive that appears to have lost every document, while the real
// data sat beside it in plaintext.
//
// Migration is deliberately manual, because doing it properly means moving onto
// a fresh volume: deleting the old files would leave their contents recoverable
// in free space.
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

// checkMemoryBacked is isMemoryBacked, indirected so a test can exercise the
// branch where the filesystem type cannot be determined. That branch decides
// whether an unverifiable platform boots, and is unreachable on Linux.
var checkMemoryBacked = isMemoryBacked

// checkWorkDirIsMemoryBacked refuses to decrypt into ordinary storage. Nothing
// else enforces the promise that plaintext never reaches persistent disk: point
// WorkDir at a normal directory and the archive is decrypted onto the medium it
// was being protected from, with every other guarantee still appearing to hold.
func (v *Vault) checkWorkDirIsMemoryBacked() error {
	if v.opts.AllowDiskWorkDir {
		return nil
	}
	if err := os.MkdirAll(v.opts.WorkDir, 0o700); err != nil {
		return err
	}
	mem, err := checkMemoryBacked(v.opts.WorkDir)
	if err != nil {
		// Cannot tell: non-Linux, or a statfs that refused. Continuing would reach the
		// same outcome the doc comment refuses, silently and on every boot, so the
		// unverifiable case is treated as the unsafe one.
		return fmt.Errorf(
			"vault: cannot verify that %s is memory-backed (%w), so there is no way from in here to tell whether decrypting into it would write every document to disk in the clear. Mount a tmpfs there (in compose: tmpfs: [\"%s:size=2g,mode=0700\"]), or set %s=1 to accept plaintext on disk",
			v.opts.WorkDir, err, v.opts.WorkDir, EnvAllowDiskWorkDir)
	}
	if !mem {
		return fmt.Errorf(
			"vault: %s is not a memory-backed filesystem, so decrypting into it would write every document to disk in the clear. Mount a tmpfs there (in compose: tmpfs: [\"%s:size=2g,mode=0700\"]), or set %s=1 to accept plaintext on disk",
			v.opts.WorkDir, v.opts.WorkDir, EnvAllowDiskWorkDir)
	}
	return nil
}

// drainWait has to fit inside the container stop grace period with room for the
// flush itself; the encrypted compose overlay allows 60s. A variable only so
// tests need not sit through it.
var drainWait = 20 * time.Second

// finalizeRetries bounds the re-flush loop that catches work landing while the
// previous flush ran.
const finalizeRetries = 2

// Finalize performs the last flush while the databases are still open.
// PocketBase triggers OnTerminate for every command, so this runs on any clean
// exit and Close then only wipes and unlocks; by the time a deferred Close runs
// the databases are closed and the snapshot would fail.
//
// It waits for in-flight work first, which is the difference between a clean
// stop being lossless and only appearing to be: PocketBase's graceful shutdown
// gives handlers one second and never waits for cron jobs, so an upload
// finishing a moment after the flush is answered 200, written into the working
// directory, and wiped with it. Then it flushes until nothing is dirty, since a
// write can land during the flush meant to capture it.
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

	v.drain()

	var err error
	for attempt := 0; ; attempt++ {
		if err = v.Flush("terminate"); err != nil {
			break
		}
		// Anything dirtied while that flush ran is not in it. The bound is there
		// because a system still taking writes at shutdown must not keep the process
		// alive indefinitely.
		if v.dirty.get() == 0 || attempt >= finalizeRetries {
			break
		}
		v.opts.Log("vault: writes landed during the shutdown flush; flushing again")
	}

	v.mu.Lock()
	v.finalized = err == nil
	v.mu.Unlock()
	if err != nil {
		v.opts.Log("vault: the shutdown flush failed, so the working directory will be kept: %v", err)
	}
}

// drain waits for handlers and worker jobs before the shutdown flush reads the
// working directory. A timeout is not fatal: the flush still captures
// everything written up to that moment, and the log line exists so a stop that
// may have clipped a write says so.
// reporting the clean shutdown it did not quite achieve.
func (v *Vault) drain() {
	if inflight.Active() == 0 {
		return
	}
	v.opts.Log("vault: waiting for %d in-flight operations before the shutdown flush", inflight.Active())

	ctx, cancel := context.WithTimeout(context.Background(), drainWait)
	defer cancel()
	if err := inflight.Wait(ctx); err != nil {
		v.opts.Log(
			"vault: %d operations were still running after %s; flushing anyway, so a write finishing now may not be in the archive",
			inflight.Active(), drainWait)
	}
}

// Close flushes, wipes the plaintext working directory, and releases the lock.
// The wipe is not tidiness: on a correct deployment the working directory is
// tmpfs and vanishes anyway, but if it ever lands on real storage the decrypted
// archive would outlive the process and defeat the whole feature.
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

// Wipe empties the working directory, which is the part that matters and the
// part that is checked. Removing the directory itself is best-effort: in the
// intended deployment WorkDir is the tmpfs mount point, and unlinking a mount
// point always fails with EBUSY, so returning that would report an alarming
// failure to remove plaintext on every clean shutdown that had in fact removed
// it.
func (v *Vault) Wipe() error {
	if v.opts.WorkDir == "" {
		return nil
	}
	if err := removeContents(v.opts.WorkDir); err != nil {
		return err
	}
	if err := os.Remove(v.opts.WorkDir); err != nil && !os.IsNotExist(err) {
		v.opts.Log("vault: emptied %s but left the directory itself in place (%v); this is expected when it is a mount point", v.opts.WorkDir, err)
	}
	return nil
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

// InstallTempDir stops the OCR and preview paths writing plaintext copies of
// documents onto real disk: seven sites across worker, preview, pdfsplit,
// pdftool, appapi and limits call os.CreateTemp with an empty dir argument, and
// in the container image /tmp is the writable overlay. os.TempDir consults the
// environment on every call and exec'd children inherit it, so one assignment
// covers all of them, poppler included.
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

// GuardDataDirFlag refuses --dir alongside an enabled vault. PocketBase parses
// it eagerly and lets it override DefaultDataDir, so a stale entrypoint would
// silently produce a fully plaintext install on the persistent volume.
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
