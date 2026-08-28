// Package staging keeps an in-memory registry of uploads that wait on disk for
// the user to confirm what should happen to them.
//
// Both ingest flows that stage an upload (the Amazon archive import and the PDF
// split) need the same lifecycle: hand out an id, let the confirmation step look
// the upload up any number of times, hold it while a background job reads it,
// then either consume it or put it back for another attempt — and sweep whatever
// the user never came back for. The registry lives for the process lifetime
// only, so a restart also has to clean up the paths an earlier process left
// behind.
//
// Entries carry a caller-defined payload (the preview the confirmation step
// renders) and a path that is either a file or a directory, which is why
// removal and orphan detection are supplied by the caller.
package staging

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config describes how one staging area lives on disk.
type Config struct {
	// TTL is how long an upload waits for confirmation before it is swept.
	TTL time.Duration
	// Remove deletes one staged path: os.Remove for a staged file, os.RemoveAll
	// for a staged directory.
	Remove func(path string) error
	// Manages reports whether an entry of the staging root belongs to this
	// registry, so a sweep never deletes anything else that lives there.
	Manages func(entry fs.DirEntry) bool
}

// Files is a Config.Manages for a registry that stages one file per upload.
func Files(entry fs.DirEntry) bool { return !entry.IsDir() }

// Directories is a Config.Manages for a registry that stages a directory per
// upload.
func Directories(entry fs.DirEntry) bool { return entry.IsDir() }

// Item is one staged upload. Everything but the guarded fields is written once,
// before the item is added, and read-only afterwards.
type Item[T any] struct {
	ID          string
	OwnerUserID string
	// Path is the staged file or directory this upload owns.
	Path      string
	ExpiresAt time.Time
	// Payload is what the confirmation step needs about this upload.
	Payload T

	// holds counts the background jobs currently reading Path, and consumed
	// records that the upload is spent, so the last job to finish is the one
	// that deletes it. Both are guarded by the registry mutex.
	holds    int
	consumed bool
}

// Registry tracks staged uploads carrying payload type T.
type Registry[T any] struct {
	cfg Config

	mu    sync.Mutex
	items map[string]*Item[T]
	// busy holds the base names of the paths a job is reading, so a long run is
	// never swept out from under itself even after Claim took its entry out.
	busy map[string]struct{}
}

// New returns an empty registry.
func New[T any](cfg Config) *Registry[T] {
	return &Registry[T]{
		cfg:   cfg,
		items: map[string]*Item[T]{},
		busy:  map[string]struct{}{},
	}
}

// Add registers a freshly staged upload.
func (r *Registry[T]) Add(item *Item[T]) {
	r.mu.Lock()
	r.items[item.ID] = item
	r.mu.Unlock()
}

// DiscardOwned spends every upload this owner still has waiting, and reports
// how many. It is what keeps a staging area from growing without bound: the
// confirmation step only ever works on the newest upload, so staging a fresh
// one makes any earlier one dead weight, and without this an account could
// keep uploading and fill the data volume before confirming anything.
//
// Paths go the moment they are idle, exactly as a discard would; an upload a
// job is still reading is spent but survives until that job lets go.
func (r *Registry[T]) DiscardOwned(ownerUserID string) int {
	r.mu.Lock()
	ids := make([]string, 0, len(r.items))
	for id, item := range r.items {
		if item.OwnerUserID == ownerUserID {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()

	discarded := 0
	for _, id := range ids {
		if item, ok := r.Claim(id, ownerUserID); ok {
			r.Release(item)
			discarded++
		}
	}
	return discarded
}

// Lookup returns a live upload without touching its lifecycle, so the preview
// and thumbnail endpoints can be called repeatedly.
func (r *Registry[T]) Lookup(uploadID, ownerUserID string) (*Item[T], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.liveLocked(uploadID, ownerUserID)
}

// Claim takes the upload out of the registry and marks it busy, so the same
// upload cannot be consumed (or discarded) twice. The caller must finish with
// Release, when the upload is spent, or Restore, to offer it again.
func (r *Registry[T]) Claim(uploadID, ownerUserID string) (*Item[T], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.liveLocked(uploadID, ownerUserID)
	if !ok {
		return nil, false
	}
	delete(r.items, item.ID)
	r.holdLocked(item)
	return item, true
}

// Hold marks the upload busy while leaving it in the registry, for a job that
// reads the staged path without consuming it — a split detection run proposes
// boundaries and the user still has to confirm the split afterwards.
//
// It is what keeps a concurrent discard, a confirmed run or the TTL sweep from
// deleting the path mid-read: those only take effect once the last holder is
// done. The caller must finish with Unhold.
func (r *Registry[T]) Hold(uploadID, ownerUserID string) (*Item[T], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.liveLocked(uploadID, ownerUserID)
	if !ok {
		return nil, false
	}
	r.holdLocked(item)
	return item, true
}

// How a finished job leaves the upload it was holding.
type settlement int

const (
	// settleConsume spends the upload: the path goes as soon as it is idle.
	settleConsume settlement = iota
	// settleOffer puts a claimed upload back for another attempt.
	settleOffer
	// settleLeave ends a hold without deciding anything, because a hold never
	// took the upload out of the registry to begin with.
	settleLeave
)

// Release ends a claim and consumes the upload: the staged path is deleted as
// soon as no other job is still reading it.
func (r *Registry[T]) Release(item *Item[T]) {
	r.settle(item, settleConsume)
}

// Restore ends a claim and offers the upload again, so a run that changed
// nothing can be retried without a re-upload. An upload something else already
// consumed stays consumed.
func (r *Registry[T]) Restore(item *Item[T]) {
	r.settle(item, settleOffer)
}

// Unhold ends a hold taken with Hold. It never re-registers the upload: a
// discard, a confirmed run or the sweep may have consumed it while the holder
// was reading, and that decision stands.
func (r *Registry[T]) Unhold(item *Item[T]) {
	r.settle(item, settleLeave)
}

func (r *Registry[T]) settle(item *Item[T], how settlement) {
	r.mu.Lock()
	item.holds--
	if how == settleConsume {
		item.consumed = true
	}
	switch {
	case item.consumed:
		delete(r.items, item.ID)
	case how == settleOffer:
		r.items[item.ID] = item
	}
	idle := item.holds <= 0
	if idle {
		delete(r.busy, filepath.Base(item.Path))
	}
	remove := idle && item.consumed
	r.mu.Unlock()

	if remove {
		r.cfg.Remove(item.Path)
	}
}

// liveLocked resolves an unexpired upload of this owner. Callers must hold r.mu.
func (r *Registry[T]) liveLocked(uploadID, ownerUserID string) (*Item[T], bool) {
	item, ok := r.items[strings.TrimSpace(uploadID)]
	if !ok || item.OwnerUserID != ownerUserID || time.Now().UTC().After(item.ExpiresAt) {
		return nil, false
	}
	return item, true
}

func (r *Registry[T]) holdLocked(item *Item[T]) {
	item.holds++
	r.busy[filepath.Base(item.Path)] = struct{}{}
}

// Sweep drops expired registry entries and any staged path left behind by an
// earlier process (the registry does not survive a restart).
func (r *Registry[T]) Sweep(root string, now time.Time) {
	r.mu.Lock()
	live := make(map[string]struct{}, len(r.items)+len(r.busy))
	for name := range r.busy {
		live[name] = struct{}{}
	}
	expired := make([]*Item[T], 0, len(r.items))
	for id, item := range r.items {
		if !now.UTC().After(item.ExpiresAt) {
			live[filepath.Base(item.Path)] = struct{}{}
			continue
		}
		delete(r.items, id)
		if item.holds > 0 {
			// A job is still reading it: the last holder does the deleting.
			item.consumed = true
			live[filepath.Base(item.Path)] = struct{}{}
			continue
		}
		expired = append(expired, item)
	}
	r.mu.Unlock()

	for _, item := range expired {
		r.cfg.Remove(item.Path)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !r.cfg.Manages(entry) {
			continue
		}
		if _, ok := live[entry.Name()]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= r.cfg.TTL {
			continue
		}
		r.cfg.Remove(filepath.Join(root, entry.Name()))
	}
}

// NewID returns an unguessable upload id.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate upload id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
