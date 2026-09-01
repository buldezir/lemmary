package fulltext

import (
	"database/sql"
	"errors"
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
// The Enqueue* methods never block the caller: they append to a FIFO task
// queue (guarded by qMu, outside the lock order above) that a single worker
// goroutine drains. One worker means tasks for the same document apply in
// enqueue order, so a delete can never be overtaken by an older upsert. The
// worker takes writeMu.RLock per task, so a rebuild pauses the queue without
// stalling the record hooks that enqueue. WaitIdle joins the queue via qCond.
type Index struct {
	mu           sync.RWMutex // held for the duration of Bleve ops; exclusive for close/swap
	writeMu      sync.RWMutex // writers RLock; Rebuild/Close take Lock
	rebuildMu    sync.Mutex   // serializes Rebuild
	idx          bleve.Index
	path         string
	versionPath  string
	needsRebuild bool

	// The chunk index is the same handle pattern one level down: passages
	// rather than documents, with a vector field. It is optional — nil
	// whenever no embedding model is bound — and everything that touches it
	// takes the locks above in the same order.
	chunkIdx         bleve.Index
	chunkPath        string
	chunkVersionPath string
	chunkRebuild     bool
	spec             VectorSpec
	source           ChunkSource

	qMu      sync.Mutex // guards queue, pending, draining, qCond; never held around Bleve ops
	qCond    *sync.Cond // lazily created under qMu; signaled when pending drops to 0
	queue    []indexTask
	pending  int // queued + in-flight tasks
	draining bool
}

type taskKind int

const (
	taskUpsert taskKind = iota
	taskDelete
	taskReindexEntity
	taskUpsertChunks
	taskDeleteChunks
	taskRebuildChunks
)

type indexTask struct {
	kind       taskKind
	app        core.App
	id         string
	collection string
	field      string
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

	chunkPath := filepath.Join(base, chunkDirName)
	chunkVersionPath := filepath.Join(base, chunkVersionName)

	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("create bleve dir: %w", err)
	}
	_ = os.RemoveAll(path + rebuildSuffix)
	_ = os.RemoveAll(chunkPath + rebuildSuffix)

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
	// The chunk index is not opened here: its mapping depends on the embedding
	// binding, which only the caller knows. SetVectorSpec follows immediately.
	i.chunkPath = chunkPath
	i.chunkVersionPath = chunkVersionPath
	return nil
}

func (i *Index) Close() error {
	i.rebuildMu.Lock()
	defer i.rebuildMu.Unlock()
	// Drain before taking writeMu: the worker needs writeMu.RLock per task.
	// Tasks enqueued after this point find a nil handle and fail harmlessly.
	i.WaitIdle()
	i.writeMu.Lock()
	defer i.writeMu.Unlock()
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.chunkIdx != nil {
		_ = i.chunkIdx.Close()
		i.chunkIdx = nil
	}
	if i.idx == nil {
		return nil
	}
	err := i.idx.Close()
	i.idx = nil
	return err
}

// WaitIdle blocks until queued async index tasks finish.
func (i *Index) WaitIdle() {
	i.qMu.Lock()
	if i.qCond == nil {
		i.qCond = sync.NewCond(&i.qMu)
	}
	for i.pending > 0 {
		i.qCond.Wait()
	}
	i.qMu.Unlock()
}

// EnqueueUpsert reloads and indexes the document after the PocketBase write commits.
func (i *Index) EnqueueUpsert(app core.App, id string) {
	id = strings.TrimSpace(id)
	if app == nil || id == "" {
		return
	}
	i.enqueue(indexTask{kind: taskUpsert, app: app, id: id})
}

func (i *Index) EnqueueDelete(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	i.enqueue(indexTask{kind: taskDelete, id: id})
}

func (i *Index) EnqueueReindexEntity(app core.App, collection, field, entityID string) {
	entityID = strings.TrimSpace(entityID)
	if app == nil || collection == "" || field == "" || entityID == "" {
		return
	}
	i.enqueue(indexTask{kind: taskReindexEntity, app: app, id: entityID, collection: collection, field: field})
}

func (i *Index) enqueue(t indexTask) {
	i.qMu.Lock()
	i.queue = append(i.queue, t)
	i.pending++
	startWorker := !i.draining
	if startWorker {
		i.draining = true
	}
	i.qMu.Unlock()
	if startWorker {
		go i.drainTasks()
	}
}

func (i *Index) drainTasks() {
	for {
		i.qMu.Lock()
		if len(i.queue) == 0 {
			i.draining = false
			i.qMu.Unlock()
			return
		}
		t := i.queue[0]
		i.queue[0] = indexTask{}
		i.queue = i.queue[1:]
		i.qMu.Unlock()

		i.runTask(t)

		i.qMu.Lock()
		i.pending--
		if i.pending == 0 && i.qCond != nil {
			i.qCond.Broadcast()
		}
		i.qMu.Unlock()
	}
}

func (i *Index) runTask(t indexTask) {
	// A rebuild takes writeMu exclusively and takes it itself, so it must not
	// run under the writer gate the other tasks hold.
	if t.kind == taskRebuildChunks {
		if _, err := i.RebuildChunks(t.app); err != nil {
			t.app.Logger().Error("chunk index rebuild failed", slog.Any("error", err))
		}
		return
	}

	i.writeMu.RLock()
	defer i.writeMu.RUnlock()
	switch t.kind {
	case taskDelete:
		_ = i.deleteUnlocked(t.id)
		// A deleted document takes its passages with it: the chunk rows are
		// gone from SQLite by now, so nothing would ever repair the index.
		if err := i.deleteChunksUnlocked(t.id); err != nil && !errors.Is(err, errChunksNotReady) {
			logChunkTaskError(t.app, "fulltext chunk delete failed", t.id, err)
		}
	case taskUpsertChunks:
		if err := i.upsertChunksUnlocked(t.app, t.id); err != nil && !errors.Is(err, errChunksNotReady) {
			logChunkTaskError(t.app, "fulltext chunk upsert failed", t.id, err)
		}
	case taskDeleteChunks:
		if err := i.deleteChunksUnlocked(t.id); err != nil && !errors.Is(err, errChunksNotReady) {
			logChunkTaskError(t.app, "fulltext chunk delete failed", t.id, err)
		}
	case taskUpsert:
		rec, err := t.app.FindRecordById("documents", t.id)
		if err != nil {
			// Only evict on a confirmed missing record; a transient DB error
			// must not silently drop a live document from search.
			if errors.Is(err, sql.ErrNoRows) {
				_ = i.deleteUnlocked(t.id)
			} else {
				t.app.Logger().Error("fulltext upsert lookup failed", slog.String("id", t.id), slog.Any("error", err))
			}
			return
		}
		if err := i.upsertUnlocked(t.app, rec); err != nil {
			t.app.Logger().Error("fulltext upsert failed", slog.String("id", t.id), slog.Any("error", err))
		}
	case taskReindexEntity:
		reindexDocumentsForEntity(t.app, i, t.collection, t.field, t.id)
	}
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

// ShouldHeal reports whether the index has drifted from SQLite. Called at
// boot: async index writes are fire-and-forget, so a crash (or a boot where
// hooks never registered) leaves a non-empty but incomplete index that an
// empty-only check would never notice.
func (i *Index) ShouldHeal(app core.App) bool {
	count, err := i.DocCount()
	if err != nil {
		return false
	}
	n, err := app.CountRecords("documents")
	if err != nil {
		return false
	}
	if uint64(n) != count {
		return true
	}
	return i.chunksShouldHeal(app)
}

// chunksShouldHeal compares the chunk index against the store the same way.
// It is what turns embeddings on for an existing archive: the first boot after
// a model is bound finds an empty chunk index over a full table and fills it.
func (i *Index) chunksShouldHeal(app core.App) bool {
	i.mu.RLock()
	src := i.source
	spec := i.spec
	ready := i.chunkIdx != nil
	rebuild := i.chunkRebuild
	i.mu.RUnlock()
	if !ready || src == nil || !spec.Valid() {
		return false
	}
	if rebuild {
		return true
	}
	stored, err := src.Count(app, spec)
	if err != nil {
		return false
	}
	indexed, err := i.ChunkCount()
	if err != nil {
		return false
	}
	return uint64(stored) != indexed
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

	// Built before the swap, so a chunk store that errors costs nothing: the
	// documents index still lands, and the chunk half is retried by the boot
	// heal rather than leaving both halves half-installed.
	chunks, chunkErr := i.buildChunkIndex(app)
	if chunkErr != nil && app != nil {
		app.Logger().Error("chunk index rebuild failed", slog.Any("error", chunkErr))
	}

	if err := i.installRebuilt(built, builtPath, versionPath, chunks, chunkErr != nil); err != nil {
		_ = os.RemoveAll(builtPath)
		if chunks != nil {
			_ = chunks.idx.Close()
			_ = os.RemoveAll(chunks.path)
		}
		return n, err
	}
	if chunks != nil {
		logChunkRebuild(app, chunks)
	}
	return n, nil
}

func (i *Index) installRebuilt(built bleve.Index, builtPath, versionPath string, chunks *chunkBuild, chunksFailed bool) error {
	if err := built.Close(); err != nil {
		if chunks != nil {
			_ = chunks.idx.Close()
			_ = os.RemoveAll(chunks.path)
		}
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
		} else {
			i.needsRebuild = true
		}
		return fmt.Errorf("remove bleve index: %w", err)
	}
	// Past this point the old index is gone: on failure leave needsRebuild set
	// so the heal path can retry instead of serving "not ready" until restart.
	if err := os.Rename(builtPath, path); err != nil {
		i.needsRebuild = true
		return fmt.Errorf("install bleve index: %w", err)
	}
	idx, err := bleve.Open(path)
	if err != nil {
		i.needsRebuild = true
		return fmt.Errorf("open rebuilt bleve index: %w", err)
	}
	i.idx = idx
	if err := os.WriteFile(versionPath, []byte(MappingVersion+"\n"), 0o644); err != nil {
		i.needsRebuild = true
		return fmt.Errorf("write mapping version: %w", err)
	}
	i.needsRebuild = false

	// The chunk half is installed last and reported through needsRebuild
	// rather than through the error: half a rebuild is still a working keyword
	// search, and the boot heal will come back for the rest.
	if chunks != nil {
		if err := i.installChunkBuild(chunks); err != nil {
			i.needsRebuild = true
			return fmt.Errorf("install chunk index: %w", err)
		}
	} else if chunksFailed {
		i.needsRebuild = true
	}
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
