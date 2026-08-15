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

const rebuildPageSize = 200

// Index is a process-wide Bleve handle. It may be passed around before Open.
type Index struct {
	mu           sync.RWMutex
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

	if err := os.WriteFile(versionPath, []byte(MappingVersion+"\n"), 0o644); err != nil {
		_ = idx.Close()
		return fmt.Errorf("write mapping version: %w", err)
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
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		rec, err := app.FindRecordById("documents", id)
		if err != nil {
			_ = i.Delete(id)
			return
		}
		if err := i.Upsert(app, rec); err != nil {
			app.Logger().Error("fulltext upsert failed", slog.String("id", id), slog.Any("error", err))
		}
	}()
}

func (i *Index) EnqueueDelete(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		_ = i.Delete(id)
	}()
}

func (i *Index) EnqueueReindexEntity(app core.App, collection, field, entityID string) {
	entityID = strings.TrimSpace(entityID)
	if app == nil || collection == "" || field == "" || entityID == "" {
		return
	}
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		reindexDocumentsForEntity(app, i, collection, field, entityID)
	}()
}

func (i *Index) Put(id string, doc map[string]any) error {
	b, err := i.bleve()
	if err != nil {
		return err
	}
	return b.Index(id, doc)
}

func (i *Index) Upsert(app core.App, rec *core.Record) error {
	if rec == nil {
		return nil
	}
	b, err := i.bleve()
	if err != nil {
		return err
	}
	return b.Index(rec.Id, Build(app, rec))
}

func (i *Index) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	b, err := i.bleve()
	if err != nil {
		return err
	}
	return b.Delete(id)
}

func (i *Index) DocCount() (uint64, error) {
	b, err := i.bleve()
	if err != nil {
		return 0, err
	}
	return b.DocCount()
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

// Rebuild wipes the on-disk index and reindexes every document from PocketBase.
func (i *Index) Rebuild(app core.App) (int, error) {
	i.WaitIdle()
	i.mu.Lock()
	path := i.path
	versionPath := i.versionPath
	if i.idx != nil {
		_ = i.idx.Close()
		i.idx = nil
	}
	i.mu.Unlock()

	if path == "" {
		return 0, fmt.Errorf("search index is not open")
	}
	if err := os.RemoveAll(path); err != nil {
		return 0, fmt.Errorf("remove bleve index: %w", err)
	}

	mapping, err := newMapping()
	if err != nil {
		return 0, fmt.Errorf("bleve mapping: %w", err)
	}
	idx, err := bleve.New(path, mapping)
	if err != nil {
		return 0, fmt.Errorf("create bleve index: %w", err)
	}

	n, err := indexAllDocuments(app, idx)
	if err != nil {
		_ = idx.Close()
		return n, err
	}
	if err := os.WriteFile(versionPath, []byte(MappingVersion+"\n"), 0o644); err != nil {
		_ = idx.Close()
		return n, fmt.Errorf("write mapping version: %w", err)
	}

	i.mu.Lock()
	i.idx = idx
	i.needsRebuild = false
	i.mu.Unlock()
	return n, nil
}

func indexAllDocuments(app core.App, idx bleve.Index) (int, error) {
	if _, err := app.FindCollectionByNameOrId("documents"); err != nil {
		return 0, fmt.Errorf("documents collection: %w", err)
	}

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
			if err := batch.Index(rec.Id, Build(app, rec)); err != nil {
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

func (i *Index) bleve() (bleve.Index, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.idx == nil {
		return nil, fmt.Errorf("search index is not ready")
	}
	return i.idx, nil
}
