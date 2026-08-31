package vault

import (
	"fmt"
	"os"
	"path/filepath"
)

// restore materialises the newest readable generation into the working
// directory.
//
// It is the only place that sets loaded=true, and it does so only after the
// working directory genuinely reflects a manifest — which is what lets Flush
// treat that flag as proof it is safe to overwrite the vault.
func (v *Vault) restore() error {
	v.mu.Lock()
	store := v.store
	mkey := v.mkey
	v.mu.Unlock()

	m, err := loadLatest(v.opts.Dir, mkey, v.opts.Log)
	if err != nil {
		return err
	}

	// Start from a clean working directory so a stale file from a previous run
	// cannot survive into the restored tree and then be flushed back as if it
	// belonged.
	if err := os.MkdirAll(v.opts.WorkDir, 0o700); err != nil {
		return err
	}
	if err := removeContents(v.opts.WorkDir); err != nil {
		return err
	}

	var restored int64
	if m != nil {
		if err := v.checkCapacity(m); err != nil {
			return err
		}
		for _, e := range m.Entries {
			id, err := e.blobID()
			if err != nil {
				return err
			}
			dst := filepath.Join(v.opts.WorkDir, filepath.FromSlash(e.Path))
			if !withinDir(v.opts.WorkDir, dst) {
				// A manifest is authenticated, so this is unreachable without
				// the master key; the check exists so a path traversal can
				// never become reachable through a future format change.
				return fmt.Errorf("%w: manifest entry %q escapes the working directory", ErrCorrupt, e.Path)
			}
			mode := os.FileMode(e.Mode).Perm()
			if mode == 0 {
				mode = 0o600
			}
			if err := store.get(id, dst, mode); err != nil {
				return err
			}
			restored += e.Size
		}
	}

	// The directories the application expects to exist, and the OS temp
	// directory we redirect into RAM.
	for _, d := range []string{"storage", "temp", osTempName} {
		if err := os.MkdirAll(filepath.Join(v.opts.WorkDir, d), 0o700); err != nil {
			return err
		}
	}

	v.mu.Lock()
	v.prev = m
	v.loaded = true
	v.mu.Unlock()

	gen := uint64(0)
	entries := 0
	if m != nil {
		gen, entries = m.Gen, len(m.Entries)
	}
	v.opts.Log("vault: restored generation=%d entries=%d bytes=%d into %s", gen, entries, restored, v.opts.WorkDir)
	return nil
}

// checkCapacity refuses to unlock when the working directory cannot hold the
// archive with room to work.
//
// Running a memory-backed data directory out of space mid-write is a far worse
// failure than declining to start, and the message has to name the number an
// operator needs to put in the tmpfs size.
func (v *Vault) checkCapacity(m *Manifest) error {
	need := m.TotalSize() * 8 / 5 // 1.6x
	avail, err := availableBytes(v.opts.WorkDir)
	if err != nil {
		// Not being able to ask is not a reason to refuse to boot.
		v.opts.Log("vault: cannot determine free space in %s: %v", v.opts.WorkDir, err)
		return nil
	}
	if avail < need {
		return fmt.Errorf(
			"vault: %s has %d MiB free but the archive needs about %d MiB; increase the tmpfs size",
			v.opts.WorkDir, avail>>20, need>>20)
	}
	return nil
}

// Verify decrypts every blob in the current generation and checks it against its
// own content address.
func (v *Vault) Verify() (int, error) {
	v.mu.Lock()
	m, store := v.prev, v.store
	v.mu.Unlock()
	if m == nil {
		return 0, nil
	}
	for _, e := range m.Entries {
		id, err := e.blobID()
		if err != nil {
			return 0, err
		}
		if err := store.verify(id); err != nil {
			return 0, fmt.Errorf("vault: entry %q: %w", e.Path, err)
		}
	}
	return len(m.Entries), nil
}

// withinDir reports whether path stays inside root.
func withinDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) &&
		!(len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator))
}
