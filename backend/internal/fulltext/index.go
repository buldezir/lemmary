package fulltext

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/pocketbase/pocketbase/core"
)

const (
	rebuildPageSize   = 200
	rebuildSuffix     = ".rebuilding"
	defaultLookupPage = 500
)

// lookupPageSize is the SQLite/Bleve page size for named-entity fan-out.
// Tests may lower it to exercise pagination.
var lookupPageSize = defaultLookupPage

// indexAllHook, if set, replaces SQLite listing during Rebuild. Tests only.
var indexAllHook func(app core.App, idx bleve.Index) (int, error)

// Index is a process-wide Bleve handle. It may be passed around before Open.
//
// Locking protocol — acquire in this order, never the reverse:
//
//		rebuildMu -> writeMu -> mu
//
//	  - rebuildMu serializes Rebuild against itself and Close.
//	  - writeMu is the writer gate: index/delete paths take RLock, Rebuild and
//	    Close take Lock so an index swap cannot land mid-write. A write that
//	    arrives during a rebuild blocks here until the new index is installed.
//	  - mu guards the idx handle itself and is held for the duration of a single
//	    Bleve operation, exclusively while closing or swapping the handle.
//
// The Enqueue* methods deliberately take writeMu.RLock on the calling goroutine
// and release it on the spawned one (via beginWrite/endWrite). That is legal for
// sync.RWMutex — it is not goroutine-owned — and is what lets Close and Rebuild
// wait for in-flight async work. Do not add a nested beginWrite inside a write
// path: a re-entrant RLock deadlocks whenever a writer is queued on writeMu.
// wg exists so WaitIdle can join spawned tasks without holding a lock.
type Index struct {
	mu           sync.RWMutex // held for the duration of Bleve ops; exclusive for close/swap
	writeMu      sync.RWMutex // writers RLock; Rebuild/Close take Lock
	rebuildMu    sync.Mutex   // serializes Rebuild
	wg           sync.WaitGroup
	idx          bleve.Index
	path         string
	versionPath  string
	needsRebuild bool
}

func New() *Index {
	return &Index{}
}

func (i *Index) Ready() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.idx != nil
}

func (i *Index) NeedsRebuild() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.needsRebuild
}

func (i *Index) Open(dataDir string) error {
	base := filepath.Join(dataDir, "bleve")
	path := filepath.Join(base, "documents")
	versionPath := filepath.Join(base, "mapping.version")

	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("create bleve dir: %w", err)
	}
	_ = os.RemoveAll(path + rebuildSuffix)

	versionOK := false
	if b, err := os.ReadFile(versionPath); err == nil {
		versionOK = strings.TrimSpace(string(b)) == MappingVersion
	}

	if _, err := os.Stat(path); err == nil && !versionOK {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove stale bleve index: %w", err)
		}
	}

	mapping, err := newMapping()
	if err != nil {
		return fmt.Errorf("bleve mapping: %w", err)
	}

	needsRebuild := !versionOK
	var idx bleve.Index
	if _, err := os.Stat(path); os.IsNotExist(err) {
		idx, err = bleve.New(path, mapping)
		if err != nil {
			return fmt.Errorf("create bleve index: %w", err)
		}
		needsRebuild = true
	} else {
		idx, err = bleve.Open(path)
		if err != nil {
			_ = os.RemoveAll(path)
			idx, err = bleve.New(path, mapping)
			if err != nil {
				return fmt.Errorf("recreate bleve index: %w", err)
			}
			needsRebuild = true
		}
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if i.idx != nil {
		_ = i.idx.Close()
	}
	i.idx = idx
	i.path = path
	i.versionPath = versionPath
	i.needsRebuild = needsRebuild
	return nil
}

func (i *Index) Close() error {
	i.rebuildMu.Lock()
	defer i.rebuildMu.Unlock()
	i.writeMu.Lock()
	defer i.writeMu.Unlock()
	i.WaitIdle()
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.idx == nil {
		return nil
	}
	err := i.idx.Close()
	i.idx = nil
	return err
}

// WaitIdle blocks until in-flight async index tasks finish.
func (i *Index) WaitIdle() {
	i.wg.Wait()
}

// EnqueueUpsert reloads and indexes the document after the PocketBase write commits.
func (i *Index) EnqueueUpsert(app core.App, id string) {
	id = strings.TrimSpace(id)
	if app == nil || id == "" {
		return
	}
	i.beginWrite()
	go func() {
		defer i.endWrite()
		rec, err := app.FindRecordById("documents", id)
		if err != nil {
			_ = i.deleteUnlocked(id)
			return
		}
		if err := i.upsertUnlocked(app, rec); err != nil {
			app.Logger().Error("fulltext upsert failed", slog.String("id", id), slog.Any("error", err))
		}
	}()
}

func (i *Index) EnqueueDelete(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	i.beginWrite()
	go func() {
		defer i.endWrite()
		_ = i.deleteUnlocked(id)
	}()
}

func (i *Index) EnqueueReindexEntity(app core.App, collection, field, entityID string) {
	entityID = strings.TrimSpace(entityID)
	if app == nil || collection == "" || field == "" || entityID == "" {
		return
	}
	i.beginWrite()
	go func() {
		defer i.endWrite()
		reindexDocumentsForEntity(app, i, collection, field, entityID)
	}()
}

func (i *Index) beginWrite() {
	i.writeMu.RLock()
	i.wg.Add(1)
}

func (i *Index) endWrite() {
	i.wg.Done()
	i.writeMu.RUnlock()
}

func (i *Index) Put(id string, doc map[string]any) error {
	i.writeMu.RLock()
	defer i.writeMu.RUnlock()
	return i.withIndex(func(b bleve.Index) error {
		return b.Index(id, doc)
	})
}

func (i *Index) Upsert(app core.App, rec *core.Record) error {
	if rec == nil {
		return nil
	}
	i.writeMu.RLock()
	defer i.writeMu.RUnlock()
	return i.upsertUnlocked(app, rec)
}

func (i *Index) upsertUnlocked(app core.App, rec *core.Record) error {
	if rec == nil {
		return nil
	}
	return i.putUnlocked(rec.Id, Build(app, rec))
}

// putUnlocked indexes a prebuilt document. Callers must already hold writeMu
// (see the lock-order note on Index).
func (i *Index) putUnlocked(id string, doc map[string]any) error {
	return i.withIndex(func(b bleve.Index) error {
		return b.Index(id, doc)
	})
}

func (i *Index) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	i.writeMu.RLock()
	defer i.writeMu.RUnlock()
	return i.deleteUnlocked(id)
}

func (i *Index) deleteUnlocked(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return i.withIndex(func(b bleve.Index) error {
		return b.Delete(id)
	})
}

func (i *Index) DocCount() (uint64, error) {
	var n uint64
	err := i.withIndex(func(b bleve.Index) error {
		var err error
		n, err = b.DocCount()
		return err
	})
	return n, err
}

// ShouldHeal reports whether the index is empty while SQLite still has documents.
func (i *Index) ShouldHeal(app core.App) bool {
	count, err := i.DocCount()
	if err != nil || count > 0 {
		return false
	}
	n, err := app.CountRecords("documents")
	return err == nil && n > 0
}

// Rebuild builds a replacement index and swaps it in only after success.
// Concurrent writers wait at writeMu so their updates land on the new index.
func (i *Index) Rebuild(app core.App) (int, error) {
	i.rebuildMu.Lock()
	defer i.rebuildMu.Unlock()
	i.writeMu.Lock()
	defer i.writeMu.Unlock()

	i.mu.RLock()
	path := i.path
	versionPath := i.versionPath
	i.mu.RUnlock()
	if path == "" {
		return 0, fmt.Errorf("search index is not open")
	}

	mapping, err := newMapping()
	if err != nil {
		return 0, fmt.Errorf("bleve mapping: %w", err)
	}

	builtPath := path + rebuildSuffix
	_ = os.RemoveAll(builtPath)
	built, err := bleve.New(builtPath, mapping)
	if err != nil {
		return 0, fmt.Errorf("create bleve rebuild index: %w", err)
	}

	n, err := indexAllDocuments(app, built)
	if err != nil {
		_ = built.Close()
		_ = os.RemoveAll(builtPath)
		return n, err
	}

	if err := i.installRebuilt(built, builtPath, versionPath); err != nil {
		_ = os.RemoveAll(builtPath)
		return n, err
	}
	return n, nil
}

func (i *Index) installRebuilt(built bleve.Index, builtPath, versionPath string) error {
	if err := built.Close(); err != nil {
		return fmt.Errorf("close rebuild index: %w", err)
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	old := i.idx
	path := i.path
	if old != nil {
		_ = old.Close()
		i.idx = nil
	}
	if err := os.RemoveAll(path); err != nil {
		if reopened, oerr := bleve.Open(path); oerr == nil {
			i.idx = reopened
		}
		return fmt.Errorf("remove bleve index: %w", err)
	}
	if err := os.Rename(builtPath, path); err != nil {
		return fmt.Errorf("install bleve index: %w", err)
	}
	idx, err := bleve.Open(path)
	if err != nil {
		return fmt.Errorf("open rebuilt bleve index: %w", err)
	}
	i.idx = idx
	if err := os.WriteFile(versionPath, []byte(MappingVersion+"\n"), 0o644); err != nil {
		i.needsRebuild = true
		return fmt.Errorf("write mapping version: %w", err)
	}
	i.needsRebuild = false
	return nil
}

func indexAllDocuments(app core.App, idx bleve.Index) (int, error) {
	if indexAllHook != nil {
		return indexAllHook(app, idx)
	}
	return indexAllDocumentsFromApp(app, idx)
}

func indexAllDocumentsFromApp(app core.App, idx bleve.Index) (int, error) {
	if _, err := app.FindCollectionByNameOrId("documents"); err != nil {
		return 0, fmt.Errorf("documents collection: %w", err)
	}

	// One cache for the whole rebuild: tags/types/correspondents repeat heavily
	// across documents, so this collapses tens of thousands of point lookups.
	names := newNameCache(app)
	batch := idx.NewBatch()
	n := 0
	offset := 0
	for {
		records, err := app.FindRecordsByFilter("documents", "", "-created", rebuildPageSize, offset)
		if err != nil {
			return n, fmt.Errorf("list documents: %w", err)
		}
		if len(records) == 0 {
			break
		}
		for _, rec := range records {
			if err := batch.Index(rec.Id, buildWith(names, rec)); err != nil {
				return n, fmt.Errorf("batch index %s: %w", rec.Id, err)
			}
			n++
			if batch.Size() >= rebuildPageSize {
				if err := idx.Batch(batch); err != nil {
					return n, fmt.Errorf("bleve batch: %w", err)
				}
				batch = idx.NewBatch()
			}
		}
		if len(records) < rebuildPageSize {
			break
		}
		offset += rebuildPageSize
	}
	if batch.Size() > 0 {
		if err := idx.Batch(batch); err != nil {
			return n, fmt.Errorf("bleve batch: %w", err)
		}
	}
	return n, nil
}

func (i *Index) withIndex(fn func(bleve.Index) error) error {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.idx == nil {
		return fmt.Errorf("search index is not ready")
	}
	return fn(i.idx)
}
