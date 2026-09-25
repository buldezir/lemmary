// Package staging keeps an in-memory registry of uploads that wait on disk for
// the user to confirm what should happen to them.
//
// The lifecycle both ingest flows need: hand out an id, let the confirmation
// step look the upload up any number of times, hold it while a background job
// reads it, then consume it or offer it again, and sweep what the user never
// came back for. The registry does not survive a restart, so a sweep also has
// to clean up paths an earlier process left behind.
//
// A staged path is either a file or a directory, which is why removal and
// orphan detection are supplied by the caller.
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

type Config struct {
	// How long an upload waits for confirmation before it is swept.
	TTL time.Duration
	// os.Remove for a staged file, os.RemoveAll for a staged directory.
	Remove func(path string) error
	// Whether an entry of the staging root belongs to this registry, so a
	// sweep never deletes anything else that lives there.
	Manages func(entry fs.DirEntry) bool
}

func Files(entry fs.DirEntry) bool { return !entry.IsDir() }

func Directories(entry fs.DirEntry) bool { return entry.IsDir() }

// Everything but the guarded fields is written once, before the item is added.
type Item[T any] struct {
	ID          string
	OwnerUserID string
	Path        string
	ExpiresAt   time.Time
	Payload     T

	// holds counts the jobs currently reading Path and consumed records that
	// the upload is spent, so the last job to finish deletes it. Both are
	// guarded by the registry mutex.
	holds    int
	consumed bool
}

type Registry[T any] struct {
	cfg Config

	mu    sync.Mutex
	items map[string]*Item[T]
	// Base names of the paths a job is reading, so a long run is never swept
	// out from under itself even after Claim took its entry out.
	busy map[string]struct{}
}

func New[T any](cfg Config) *Registry[T] {
	return &Registry[T]{
		cfg:   cfg,
		items: map[string]*Item[T]{},
		busy:  map[string]struct{}{},
	}
}

func (r *Registry[T]) Add(item *Item[T]) {
	r.mu.Lock()
	r.items[item.ID] = item
	r.mu.Unlock()
}

// DiscardOwned spends every upload this owner still has waiting. It keeps the
// staging area bounded: the confirmation step only works on the newest upload,
// so without this an account could fill the data volume before confirming
// anything. An upload a job is reading is spent but survives until it lets go.
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

// Does not touch the lifecycle, so the preview and thumbnail endpoints can be
// called repeatedly.
func (r *Registry[T]) Lookup(uploadID, ownerUserID string) (*Item[T], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.liveLocked(uploadID, ownerUserID)
}

// Takes the upload out of the registry and marks it busy, so it cannot be
// consumed twice. The caller must finish with Release or Restore.
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

// Marks the upload busy while leaving it in the registry, for a job that reads
// the staged path without consuming it. It keeps a concurrent discard or the
// TTL sweep from deleting the path mid-read. Finish with Unhold.
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
	// The path goes as soon as it is idle.
	settleConsume settlement = iota
	// Puts a claimed upload back for another attempt.
	settleOffer
	// Decides nothing: a hold never took the upload out of the registry.
	settleLeave
)

// Consumes the upload: the path is deleted once no other job is reading it.
func (r *Registry[T]) Release(item *Item[T]) {
	r.settle(item, settleConsume)
}

// Offers the upload again, so a run that changed nothing can be retried
// without a re-upload. One something else consumed stays consumed.
func (r *Registry[T]) Restore(item *Item[T]) {
	r.settle(item, settleOffer)
}

// Never re-registers the upload: something may have consumed it while the
// holder was reading, and that decision stands.
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
		_ = r.cfg.Remove(item.Path)
	}
}

// Callers must hold r.mu.
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

// Drops expired entries and any staged path an earlier process left behind.
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
		_ = r.cfg.Remove(item.Path)
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
		_ = r.cfg.Remove(filepath.Join(root, entry.Name()))
	}
}

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate upload id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
