package fulltext

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/pocketbase/pocketbase/core"
)

// chunkBatchSize: the vector index is trained per segment, so a segment built
// from a thousand vectors searches better than one built from twenty.
const chunkBatchSize = 1000

// Live beside the documents index under bleve/, which vault.excludedPrefixes
// already excludes as derived data.
const (
	chunkDirName     = "chunks"
	chunkVersionName = "chunks.version"
)

// Chunk is one embedded passage on its way into the index. Text may be empty
// when the stored offsets no longer fit the document's text; the chunk is still
// indexed because its vector still answers, and the passage layer falls back to
// a highlight fragment.
type Chunk struct {
	DocumentID string
	UserID     string
	Ord        int
	Page       int
	StartByte  int
	EndByte    int
	Text       string
	Vector     []float32
}

// ChunkSource is the durable store the index can be rebuilt from at any time.
// app may be nil for sources that do not need one, which lets the index be
// tested without an app.
type ChunkSource interface {
	// Spec is the embedding binding in force. False means dense retrieval is
	// off, and the chunk index is then removed rather than kept stale.
	Spec(app core.App) (VectorSpec, bool)
	// ForDocument returns one document's chunks for spec, in ordinal order.
	ForDocument(app core.App, documentID string, spec VectorSpec) ([]Chunk, error)
	// ForEach walks every chunk stored for spec, grouped by document.
	ForEach(app core.App, spec VectorSpec, fn func(Chunk) error) error
	Count(app core.App, spec VectorSpec) (int, error)
}

// SetChunkSource is called once from wiring, before Open; without it the chunk
// index stays off.
func (i *Index) SetChunkSource(src ChunkSource) {
	i.mu.Lock()
	i.source = src
	i.mu.Unlock()
}

func (i *Index) SourceSpec(app core.App) (VectorSpec, bool) {
	i.mu.RLock()
	src := i.source
	i.mu.RUnlock()
	if src == nil {
		return VectorSpec{}, false
	}
	spec, ok := src.Spec(app)
	return spec.normalized(), ok
}

func (i *Index) ChunksReady() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.chunkIdx != nil
}

func (i *Index) VectorSpec() VectorSpec {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.spec
}

func (i *Index) ChunkCount() (uint64, error) {
	var n uint64
	err := i.withChunkIndex(func(b bleve.Index) error {
		var err error
		n, err = b.DocCount()
		return err
	})
	return n, err
}

// SetVectorSpec opens, wipes or removes the chunk index as needed, and is safe
// to call repeatedly with the same spec. Called again on every settings reload
// because the dimension count is not knowable until the provider has answered
// once. It never rebuilds: a wiped index is flagged and EnqueueChunkRebuild
// fills it in the background, so a settings save never waits for an archive.
func (i *Index) SetVectorSpec(spec VectorSpec, ok bool) error {
	spec = spec.normalized()

	i.mu.RLock()
	base := i.chunkPath
	current := i.spec
	open := i.chunkIdx != nil
	i.mu.RUnlock()
	if base == "" {
		// Not open yet; the bootstrap hook sets the spec straight after Open.
		return nil
	}
	if !ok || !spec.Valid() {
		return i.disableChunks()
	}
	if open && current == spec {
		return nil
	}
	return i.openChunks(spec)
}

// disableChunks removes the directory too: inside an encrypted instance it is
// tmpfs that something else can use.
func (i *Index) disableChunks() error {
	i.writeMu.Lock()
	defer i.writeMu.Unlock()
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.chunkIdx != nil {
		_ = i.chunkIdx.Close()
		i.chunkIdx = nil
	}
	i.spec = VectorSpec{}
	i.chunkRebuild = false
	if i.chunkPath == "" {
		return nil
	}
	_ = os.Remove(i.chunkVersionPath)
	if err := os.RemoveAll(i.chunkPath); err != nil {
		return fmt.Errorf("remove chunk index: %w", err)
	}
	return nil
}

// openChunks wipes first when the version file does not match. Only the chunk
// index is touched: a model switch must not cost the archive its keyword search.
func (i *Index) openChunks(spec VectorSpec) error {
	i.writeMu.Lock()
	defer i.writeMu.Unlock()

	i.mu.RLock()
	path := i.chunkPath
	versionPath := i.chunkVersionPath
	i.mu.RUnlock()
	if path == "" {
		return nil
	}

	mapping, err := newChunkMapping(spec)
	if err != nil {
		return err
	}

	versionOK := false
	if b, err := os.ReadFile(versionPath); err == nil {
		versionOK = strings.TrimSpace(string(b)) == spec.version()
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if i.chunkIdx != nil {
		_ = i.chunkIdx.Close()
		i.chunkIdx = nil
	}
	_ = os.RemoveAll(path + rebuildSuffix)

	if !versionOK {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove stale chunk index: %w", err)
		}
	}

	needsRebuild := !versionOK
	var idx bleve.Index
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		idx, err = bleve.New(path, mapping)
		if err != nil {
			return fmt.Errorf("create chunk index: %w", err)
		}
		needsRebuild = true
	} else {
		idx, err = bleve.Open(path)
		if err != nil {
			_ = os.RemoveAll(path)
			idx, err = bleve.New(path, mapping)
			if err != nil {
				return fmt.Errorf("recreate chunk index: %w", err)
			}
			needsRebuild = true
		}
	}

	i.chunkIdx = idx
	i.spec = spec
	i.chunkRebuild = needsRebuild
	if needsRebuild {
		// A version file over an empty index would make the next boot believe
		// it is complete.
		_ = os.Remove(versionPath)
	}
	return nil
}

func (i *Index) ChunksNeedRebuild() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.chunkIdx != nil && i.chunkRebuild
}

// EnqueueChunkRebuild is a no-op unless the index is empty after a spec change,
// so callers can fire it after every settings reload without checking.
func (i *Index) EnqueueChunkRebuild(app core.App) {
	if app == nil || !i.ChunksNeedRebuild() {
		return
	}
	// The flag stays set until the rebuild finishes, so a settings page saved
	// twice would otherwise queue the same full pass twice.
	i.qMu.Lock()
	for _, queued := range i.queue {
		if queued.kind == taskRebuildChunks {
			i.qMu.Unlock()
			return
		}
	}
	i.qMu.Unlock()
	i.enqueue(indexTask{kind: taskRebuildChunks, app: app})
}

// ChunksReplaced implements the embedstore listener.
func (i *Index) ChunksReplaced(app core.App, documentID string) {
	documentID = strings.TrimSpace(documentID)
	if documentID == "" || !i.ChunksReady() {
		return
	}
	i.enqueue(indexTask{kind: taskUpsertChunks, app: app, id: documentID})
}

// ChunksDeleted implements the embedstore listener.
func (i *Index) ChunksDeleted(documentID string) {
	documentID = strings.TrimSpace(documentID)
	if documentID == "" || !i.ChunksReady() {
		return
	}
	i.enqueue(indexTask{kind: taskDeleteChunks, id: documentID})
}

// upsertChunksUnlocked reindexes one document's chunks. Callers hold writeMu.
func (i *Index) upsertChunksUnlocked(app core.App, documentID string) error {
	i.mu.RLock()
	src := i.source
	spec := i.spec
	ready := i.chunkIdx != nil
	i.mu.RUnlock()
	if src == nil || !ready || !spec.Valid() {
		return nil
	}

	chunks, err := src.ForDocument(app, documentID, spec)
	if err != nil {
		return fmt.Errorf("load chunks for %s: %w", documentID, err)
	}

	fresh := make(map[string]struct{}, len(chunks))
	return i.withChunkIndex(func(b bleve.Index) error {
		batch := b.NewBatch()
		for _, c := range chunks {
			c.DocumentID = documentID
			id := chunkDocID(documentID, c.Ord)
			fresh[id] = struct{}{}
			if err := batch.Index(id, chunkDocument(c)); err != nil {
				return fmt.Errorf("batch chunk %s: %w", id, err)
			}
		}
		// A re-embed after an edit produces a different number of chunks at
		// different offsets, so whatever the previous run left behind past the
		// new tail has to go: those ordinals describe text that no longer
		// exists.
		stale, err := idsByKeyword(b, FieldChunkDocumentID, documentID)
		if err != nil {
			return err
		}
		for _, id := range stale {
			if _, keep := fresh[id]; keep {
				continue
			}
			batch.Delete(id)
		}
		if batch.Size() == 0 {
			return nil
		}
		return b.Batch(batch)
	})
}

// deleteChunksUnlocked removes every chunk of one document. Callers hold writeMu.
func (i *Index) deleteChunksUnlocked(documentID string) error {
	documentID = strings.TrimSpace(documentID)
	if documentID == "" || !i.ChunksReady() {
		return nil
	}
	return i.withChunkIndex(func(b bleve.Index) error {
		ids, err := idsByKeyword(b, FieldChunkDocumentID, documentID)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		batch := b.NewBatch()
		for _, id := range ids {
			batch.Delete(id)
		}
		return b.Batch(batch)
	})
}

// RebuildChunks refills the chunk index alone: on a model switch the keyword
// half of search keeps serving throughout.
func (i *Index) RebuildChunks(app core.App) (int, error) {
	i.rebuildMu.Lock()
	defer i.rebuildMu.Unlock()
	i.writeMu.Lock()
	defer i.writeMu.Unlock()

	built, err := i.buildChunkIndex(app)
	if err != nil {
		return 0, err
	}
	if built == nil {
		return 0, nil
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.installChunkBuild(built); err != nil {
		return built.indexed, err
	}
	logChunkRebuild(app, built)
	return built.indexed, nil
}

type chunkBuild struct {
	idx     bleve.Index
	path    string
	spec    VectorSpec
	loaded  int
	indexed int
	stored  int
}

// buildChunkIndex returns nil, nil when dense retrieval is off.
func (i *Index) buildChunkIndex(app core.App) (*chunkBuild, error) {
	i.mu.RLock()
	src := i.source
	spec := i.spec
	path := i.chunkPath
	i.mu.RUnlock()
	if src == nil || path == "" || !spec.Valid() {
		return nil, nil
	}

	mapping, err := newChunkMapping(spec)
	if err != nil {
		return nil, err
	}

	builtPath := path + rebuildSuffix
	_ = os.RemoveAll(builtPath)
	built, err := bleve.New(builtPath, mapping)
	if err != nil {
		return nil, fmt.Errorf("create chunk rebuild index: %w", err)
	}

	b := &chunkBuild{idx: built, path: builtPath, spec: spec}
	if n, err := src.Count(app, spec); err == nil {
		b.stored = n
	}

	batch := built.NewBatch()
	flush := func() error {
		if batch.Size() == 0 {
			return nil
		}
		if err := built.Batch(batch); err != nil {
			return fmt.Errorf("chunk batch: %w", err)
		}
		batch = built.NewBatch()
		return nil
	}

	err = src.ForEach(app, spec, func(c Chunk) error {
		b.loaded++
		if err := batch.Index(chunkDocID(c.DocumentID, c.Ord), chunkDocument(c)); err != nil {
			return fmt.Errorf("batch chunk %s:%d: %w", c.DocumentID, c.Ord, err)
		}
		b.indexed++
		if batch.Size() >= chunkBatchSize {
			return flush()
		}
		return nil
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		_ = built.Close()
		_ = os.RemoveAll(builtPath)
		return nil, err
	}
	return b, nil
}

// installChunkBuild swaps the built chunk index in. Callers hold rebuildMu,
// writeMu and mu.
func (i *Index) installChunkBuild(b *chunkBuild) error {
	if err := b.idx.Close(); err != nil {
		_ = os.RemoveAll(b.path)
		return fmt.Errorf("close chunk rebuild index: %w", err)
	}
	if i.chunkIdx != nil {
		_ = i.chunkIdx.Close()
		i.chunkIdx = nil
	}
	if err := os.RemoveAll(i.chunkPath); err != nil {
		i.chunkRebuild = true
		return fmt.Errorf("remove chunk index: %w", err)
	}
	if err := os.Rename(b.path, i.chunkPath); err != nil {
		i.chunkRebuild = true
		return fmt.Errorf("install chunk index: %w", err)
	}
	idx, err := bleve.Open(i.chunkPath)
	if err != nil {
		i.chunkRebuild = true
		return fmt.Errorf("open rebuilt chunk index: %w", err)
	}
	i.chunkIdx = idx
	i.spec = b.spec
	if err := os.WriteFile(i.chunkVersionPath, []byte(b.spec.version()+"\n"), 0o644); err != nil {
		i.chunkRebuild = true
		return fmt.Errorf("write chunk version: %w", err)
	}
	i.chunkRebuild = false
	return nil
}

// loaded and indexed differ only when Bleve refused a chunk, which it does
// silently for a vector of the wrong length: the only place that is visible.
func logChunkRebuild(app core.App, b *chunkBuild) {
	if app == nil || b == nil {
		return
	}
	app.Logger().Info("chunk index rebuilt",
		slog.String("model", b.spec.Model),
		slog.Int("dims", b.spec.Dims),
		slog.Int("stored", b.stored),
		slog.Int("loaded", b.loaded),
		slog.Int("indexed", b.indexed),
	)
}

func chunkDocument(c Chunk) map[string]any {
	return map[string]any{
		FieldChunkDocumentID: c.DocumentID,
		FieldChunkUser:       c.UserID,
		FieldChunkOrd:        float64(c.Ord),
		FieldChunkPage:       float64(c.Page),
		FieldChunkStart:      float64(c.StartByte),
		FieldChunkEnd:        float64(c.EndByte),
		FieldChunkText:       chunkText(c.Text),
		FieldChunkVector:     encodeVectorBase64(c.Vector),
	}
}

// bleve's vector_base64 field decodes little-endian float32, standard base64:
// byte for byte the layout embedstore keeps in SQLite.
func encodeVectorBase64(vec []float32) string {
	if len(vec) == 0 {
		return ""
	}
	raw := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(v))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// Zero-padded so one document's ids sort in ordinal order in Bleve tooling.
func chunkDocID(documentID string, ord int) string {
	return fmt.Sprintf("%s:%05d", documentID, ord)
}

func splitChunkDocID(id string) (string, int, bool) {
	at := strings.LastIndexByte(id, ':')
	if at <= 0 {
		return "", 0, false
	}
	ord, err := strconv.Atoi(id[at+1:])
	if err != nil {
		return "", 0, false
	}
	return id[:at], ord, true
}

func (i *Index) withChunkIndex(fn func(bleve.Index) error) error {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.chunkIdx == nil {
		return errChunksNotReady
	}
	return fn(i.chunkIdx)
}
