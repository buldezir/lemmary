package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ErrNotLoaded reports a flush attempted before the vault was materialised.
var ErrNotLoaded = errors.New("vault: refusing to flush a vault that was never loaded")

// failPoint lets tests abort a flush at a precise step to prove the commit
// ordering survives a crash at every stage.
type failPoint string

const (
	failAfterSomeBlobs   failPoint = "after-some-blobs"
	failAfterAllBlobs    failPoint = "after-all-blobs"
	failBeforeManifest   failPoint = "before-manifest"
	failAfterManifest    failPoint = "after-manifest"
	failBeforeCurrent    failPoint = "before-current"
	failAfterCurrent     failPoint = "after-current"
	errInjectedFailPoint           = "vault: injected failure at %s"
)

// Flush commits the working directory to the vault.
//
// The commit protocol, and why it needs no journal:
//
//  1. Blobs are content-addressed and immutable, and every one is fsynced and
//     renamed into place *before* any manifest names it.
//  2. The manifest is fsynced and renamed *before* CURRENT advances to it.
//
// So a crash at any point leaves either the previous complete generation or the
// new one. A partially written blob set is unreferenced garbage that the next
// GC collects; a partially written manifest never becomes current. Torn state is
// not merely unlikely, it is unrepresentable.
func (v *Vault) Flush(reason string) error {
	return v.flush(reason, "")
}

func (v *Vault) flush(reason string, fail failPoint) error {
	if !v.Enabled() {
		return nil
	}
	v.flushMu.Lock()
	defer v.flushMu.Unlock()

	// The gate that makes the whole design safe: never write a manifest built
	// from a working directory we did not populate from this vault.
	if !v.Loaded() {
		return ErrNotLoaded
	}

	started := nowUnixNano()
	stage := filepath.Join(v.opts.WorkDir, stageDirName)
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	v.mu.Lock()
	prev := v.prev
	store := v.store
	mkey := v.mkey
	snap := v.snap
	v.mu.Unlock()

	// Step 1: a consistent database snapshot, taken *before* walking storage.
	//
	// The order is load-bearing. Snapshotting the database first means the walk
	// may pick up files uploaded after the snapshot: those become blobs no row
	// references, which is harmless. The reverse order would produce a database
	// referencing files the walk never saw — a document whose PDF is missing.
	if snap != nil {
		if err := snap.SnapshotDatabases(stage); err != nil {
			return fmt.Errorf("vault: snapshot databases: %w", err)
		}
	} else if prev.hasDatabases() {
		// No snapshotter, but the generation we are about to replace had
		// databases in it. Committing here would write a manifest listing the
		// documents and none of the metadata, and the very next unlock would
		// restore an archive with no database at all.
		//
		// This is reachable without anything going wrong. The snapshotter is
		// installed from OnBootstrap, and PocketBase skips bootstrap entirely
		// for `--help`, `--version` and any unknown command — while OnTerminate
		// still fires for all of them, which is what calls Finalize. The shrink
		// guard does not catch it either: dropping two database entries out of
		// hundreds of files is nowhere near halving the archive.
		return fmt.Errorf(
			"vault: refusing to flush generation %d without a database snapshot; the previous generation has one, "+
				"so committing would leave the archive with documents and no metadata. This flush is running in a "+
				"process that never opened the databases",
			prev.Gen+1)
	}

	prevIdx := prev.index()
	// Anything whose mtime is at or after the moment the previous generation was
	// captured is "racily clean" and must be re-hashed regardless of what the
	// index says (see reuseSafe below).
	var raceFloor int64
	if prev != nil {
		raceFloor = prev.Created
	}
	entries, written, reused, err := v.collect(stage, store, prevIdx, raceFloor, fail)
	if err != nil {
		return err
	}
	if fail == failAfterAllBlobs {
		return fmt.Errorf(errInjectedFailPoint, fail)
	}

	// Step 2: the shrink guard. A flush that would drop most of the archive is
	// far more likely to be a bug — an empty working directory, a failed
	// snapshot, a mount that vanished — than a genuine mass deletion.
	if prev != nil && !v.opts.AllowShrink {
		if before, after := len(prev.Entries), len(entries); before > 0 && after*2 < before {
			return fmt.Errorf(
				"vault: refusing to flush, entry count would fall %d -> %d; set AllowShrink if this is intended",
				before, after)
		}
	}

	gen := uint64(1)
	if prev != nil {
		gen = prev.Gen + 1
	}
	m := &Manifest{Version: manifestVersion, Gen: gen, Created: started, Entries: entries}

	if fail == failBeforeManifest {
		return fmt.Errorf(errInjectedFailPoint, fail)
	}
	if err := writeManifest(v.opts.Dir, m, mkey); err != nil {
		return err
	}
	if fail == failAfterManifest {
		return fmt.Errorf(errInjectedFailPoint, fail)
	}

	// Step 3: CURRENT advances last. Until this line lands, the previous
	// generation is still the committed one.
	if fail == failBeforeCurrent {
		return fmt.Errorf(errInjectedFailPoint, fail)
	}
	if err := writeCurrent(v.opts.Dir, gen, mkey); err != nil {
		return err
	}
	if fail == failAfterCurrent {
		return fmt.Errorf(errInjectedFailPoint, fail)
	}

	v.mu.Lock()
	v.prev = m
	v.mu.Unlock()
	v.dirty.reset()
	v.flushes.add(1)

	collected, gcErr := v.collectGarbage()
	if gcErr != nil {
		// A failed GC leaves dead blobs on disk. That wastes space but cannot
		// lose data, so it must not fail the flush that already committed.
		v.opts.Log("vault: generation %d committed but garbage collection failed: %v", gen, gcErr)
	}

	v.opts.Log("vault: flushed (%s) generation=%d entries=%d new=%d reused=%d gc=%d in %dms",
		reason, gen, len(entries), written, reused, collected, (nowUnixNano()-started)/1e6)
	return nil
}

// collect builds the entry list, sealing files that changed and reusing blob
// addresses for those that did not.
func (v *Vault) collect(stage string, store *blobStore, prevIdx map[string]Entry, raceFloor int64, fail failPoint) ([]Entry, int, int, error) {
	var (
		entries []Entry
		written int
		reused  int
	)

	add := func(root, rel string, info os.FileInfo) error {
		// The unchanged-file fast path, which is what keeps a steady-state flush
		// proportional to new data rather than to the whole archive.
		if e, ok := prevIdx[rel]; ok && reuseSafe(e, info, raceFloor) {
			if id, err := e.blobID(); err == nil && store.has(id) {
				entries = append(entries, e)
				reused++
				return nil
			}
		}

		id, didWrite, err := store.put(filepath.Join(root, rel))
		if err != nil {
			return err
		}
		if didWrite {
			written++
			if fail == failAfterSomeBlobs && written >= 1 {
				return fmt.Errorf(errInjectedFailPoint, fail)
			}
		}
		entries = append(entries, Entry{
			Path:  filepath.ToSlash(rel),
			Size:  info.Size(),
			MTime: info.ModTime().UnixNano(),
			Mode:  uint32(info.Mode().Perm()),
			Blob:  fmt.Sprintf("%x", id[:]),
		})
		return nil
	}

	// The staged database snapshots first, under their canonical names.
	for _, name := range databaseFiles {
		info, err := os.Stat(filepath.Join(stage, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, 0, 0, err
		}
		if err := add(stage, name, info); err != nil {
			return nil, 0, 0, err
		}
	}

	// Then everything else in the working directory.
	err := filepath.Walk(v.opts.WorkDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				// A file deleted mid-walk is normal on a live system.
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(v.opts.WorkDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if isExcluded(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if addErr := add(v.opts.WorkDir, rel, info); addErr != nil {
			if os.IsNotExist(errors.Unwrap(addErr)) || os.IsNotExist(addErr) {
				return nil
			}
			return addErr
		}
		return nil
	})
	if err != nil {
		return nil, 0, 0, err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, written, reused, nil
}

// mtimeGranularityGuard is how far back from a capture an mtime must be before
// the unchanged-file cache will trust it.
//
// Filesystem timestamp granularity varies wildly — this kernel updates mtime on
// a ~4ms tick, ext3 and several network filesystems only manage one second — and
// the guard has to exceed whatever the vault is actually running on. Two seconds
// covers every case in practice. The cost is re-hashing files touched in the
// couple of seconds before a flush, which is a handful of recent uploads sitting
// in RAM, and is bounded regardless of archive size.
const mtimeGranularityGuard = 2 * int64(time.Second)

// reuseSafe reports whether a file can be assumed unchanged since the previous
// generation.
//
// Size and mtime alone are not enough, and the failure mode is silent data loss
// rather than an error: two writes of the same length landing inside one
// filesystem timestamp tick are indistinguishable, so the vault would go on
// persisting the stale blob while the working copy said otherwise. This is the
// "racily clean" hazard git has in its index, and it is not hypothetical — it
// reproduces in milliseconds on an ordinary rewrite.
//
// The fix is to trust an entry only when its mtime is comfortably older than the
// moment the previous generation was captured. Anything more recent is re-read
// and re-hashed, which for a memory-backed working directory costs no disk I/O.
func reuseSafe(e Entry, info os.FileInfo, raceFloor int64) bool {
	mtime := info.ModTime().UnixNano()
	if e.Size != info.Size() || e.MTime != mtime {
		return false
	}
	return raceFloor > 0 && mtime+mtimeGranularityGuard < raceFloor
}

// collectGarbage prunes old generations and deletes unreferenced blobs.
func (v *Vault) collectGarbage() (int, error) {
	keep, err := pruneGenerations(v.opts.Dir, v.opts.KeepGenerations)
	if err != nil {
		return 0, err
	}
	v.mu.Lock()
	mkey := v.mkey
	store := v.store
	v.mu.Unlock()

	live, err := liveBlobs(v.opts.Dir, mkey, keep)
	if err != nil {
		return 0, err
	}
	return store.gc(live)
}

// MarkDirty records that something changed, returning the pending count.
func (v *Vault) MarkDirty() int64 {
	if !v.Enabled() {
		return 0
	}
	return v.dirty.add(1)
}
