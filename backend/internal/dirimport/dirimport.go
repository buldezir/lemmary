// Package dirimport turns a watched directory (INGEST_DIR) into documents: a
// cron walks it, every storable file becomes a document owned by the configured
// account, and the subfolders it sat in become its tags.
package dirimport

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/router"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/inflight"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/worker"
	"lemmary/backend/internal/zipimport"
)

const EnvDir = "INGEST_DIR"

// A file younger than this may still be being written; the next scan gets it.
const settleAge = 30 * time.Second

type Result struct {
	Created int
	// Skipped files are already in the library (same checksum).
	Skipped int
	Failed  int
}

type Scanner struct {
	app     core.App
	rt      *config.Runtime
	dir     string
	running atomic.Bool
	durable func(time.Time) bool
	// Files this process already decided about, by size and mtime, so neither
	// mode reopens them every scan. Only the scan goroutine touches these maps.
	seen map[string]string
	// seenKey is the owner and delete flag seen was built under.
	seenKey string
	// Delete mode: consumed originals waiting for encryption at rest to seal
	// their document, by when it was saved.
	pending map[string]time.Time
	// Failures already logged, so a file that keeps failing warns once.
	warned map[string]bool
}

func newScanner(app core.App, rt *config.Runtime, dir string) *Scanner {
	return &Scanner{app: app, rt: rt, dir: dir, durable: inflight.Durable, seen: map[string]string{},
		pending: map[string]time.Time{}, warned: map[string]bool{}}
}

const cronJob = "dir_ingest"

// Register schedules the scan. The interval is a setting, so the job is
// re-added with a new expression on every settings reload; the schedule is then
// also what PocketBase Admin -> Crons shows.
func Register(app core.App, rt *config.Runtime, dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	s := newScanner(app, rt, dir)
	app.Cron().MustAdd(cronJob, cronExpr(config.DefaultIngestDirIntervalMin), s.tick)
	rt.OnReload(func(_ core.App, snap config.Snapshot) {
		expr := cronExpr(snap.Cfg.IngestDirIntervalMin)
		app.Cron().Remove(cronJob)
		app.Cron().MustAdd(cronJob, expr, s.tick)
	})
	app.Logger().Info("consume folder registered", "dir", dir)
}

// cronExpr renders an interval config.ValidIngestInterval accepted: every N
// minutes under an hour, every N/60 hours under a day, once a day at 1440.
func cronExpr(minutes int) string {
	switch {
	case minutes <= 1:
		return "* * * * *"
	case minutes < 60:
		return fmt.Sprintf("*/%d * * * *", minutes)
	case minutes < config.MaxIngestDirIntervalMin:
		return fmt.Sprintf("0 */%d * * *", minutes/60)
	default:
		return "0 0 * * *"
	}
}

func (s *Scanner) tick() {
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	defer s.running.Store(false)
	// With encryption at rest, a write during the shutdown seal is lost unless
	// the flusher knows to wait for it.
	defer inflight.Begin()()

	res := s.Scan(s.rt.Snapshot().Cfg, time.Now())
	if res.Created+res.Skipped+res.Failed > 0 {
		s.app.Logger().Info("consume folder scanned", "dir", s.dir,
			"created", res.Created, "skipped", res.Skipped, "failed", res.Failed)
	}
}

// Scan walks the folder once. now is what the settle check reads.
func (s *Scanner) Scan(cfg config.Config, now time.Time) Result {
	var res Result
	logger := s.app.Logger()

	ownerID := s.resolveOwner(cfg.IngestDirOwner)
	if ownerID == "" {
		return res
	}
	keep := !cfg.IngestDirDeleteOriginal
	// What was skipped for one owner or in keep mode is a decision for that
	// configuration only: a new owner has none of these files, and delete mode
	// still owes the folder a clean-up.
	if key := fmt.Sprintf("%s|%t", ownerID, keep); key != s.seenKey {
		s.seen = map[string]string{}
		s.pending = map[string]time.Time{}
		s.warned = map[string]bool{}
		s.seenKey = key
	}
	defer s.removeSealed()

	// WalkDir does not descend into a root that is itself a symlink.
	root, err := filepath.EvalSymlinks(s.dir)
	if err != nil {
		logger.Warn("consume folder: unreadable", "dir", s.dir, "error", err)
		return res
	}
	collection, err := s.app.FindCollectionByNameOrId("documents")
	if err != nil {
		logger.Warn("consume folder: documents collection", "error", err)
		return res
	}
	tags, err := loadTagKeys(s.app, ownerID)
	if err != nil {
		logger.Warn("consume folder: load tags failed", "error", err)
		return res
	}
	var ledger map[string]string
	if keep {
		if ledger, err = loadLedger(s.app, ownerID); err != nil {
			logger.Warn("consume folder: load ledger failed", "error", err)
			return res
		}
	}

	consumed := func(path, rel, stamp string) {
		s.seen[path] = stamp
		if !keep {
			s.pending[path] = time.Now()
			return
		}
		if err := recordLedger(s.app, ownerID, rel, stamp); err != nil {
			logger.Warn("consume folder: ledger write failed", "path", path, "error", err)
		}
	}

	stop := errors.New("stop")
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			s.warnOnce(path, "consume folder: unreadable entry", "path", path, "error", err)
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 || !zipimport.Storable(filepath.Ext(d.Name())) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 || now.Sub(info.ModTime()) < settleAge {
			return nil
		}
		stamp := fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		if s.seen[path] == stamp {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if ledger[rel] == stamp {
			s.seen[path] = stamp
			return nil
		}
		if info.Size() > models.MaxFileBytes {
			res.Failed++
			s.seen[path] = stamp
			logger.Warn("consume folder: file exceeds the document size limit", "path", path, "size", info.Size())
			return nil
		}
		file, err := filesystem.NewFileFromPath(path)
		if err != nil {
			s.warnOnce(path+"|"+stamp, "consume folder: read failed", "path", path, "error", err)
			return nil
		}
		file.Reader = regularFile(path)

		tagIDs, created, err := s.tagsFor(ownerID, rel, tags)
		if err != nil {
			logger.Warn("consume folder: tag lookup failed", "path", path, "error", err)
			return nil
		}
		err = zipimport.CreateDocument(s.app, collection, ownerID, file, tagIDs)
		if err != nil {
			s.dropTags(created, tags)
		}
		var dup *duplicates.ErrDuplicate
		switch {
		case err == nil:
			res.Created++
			consumed(path, rel, stamp)
		case errors.As(err, &dup):
			res.Skipped++
			consumed(path, rel, stamp)
		case roomExhausted(err):
			logger.Warn("consume folder: limit reached, stopping this scan", "error", err)
			return stop
		default:
			res.Failed++
			// A validation refusal (wrong content for the extension, over a
			// per-file limit) is about the file and stays refused; anything else
			// may be transient and is retried next scan.
			if rejected(err) {
				s.seen[path] = stamp
			}
			s.warnOnce(path+"|"+stamp, "consume folder: import failed", "path", path, "error", err)
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, stop) {
		logger.Warn("consume folder: walk failed", "dir", s.dir, "error", walkErr)
	}
	return res
}

// removeSealed deletes the originals whose documents a hard kill can no longer
// lose. Without encryption at rest that is all of them, straight away.
func (s *Scanner) removeSealed() {
	failed := false
	for path, saved := range s.pending {
		if !s.durable(saved) {
			continue
		}
		delete(s.pending, path)
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) && !failed {
			failed = true
			s.app.Logger().Warn("consume folder: delete original failed; keeping files", "path", path, "error", err)
		}
	}
}

func (s *Scanner) warnOnce(key, msg string, args ...any) {
	if s.warned[key] {
		return
	}
	s.warned[key] = true
	s.app.Logger().Warn(msg, args...)
}

// resolveOwner is the configured account, else the oldest admin's paired users
// row. Empty before setup has run, which is not an error.
func (s *Scanner) resolveOwner(configured string) string {
	if configured != "" {
		if _, err := s.app.FindRecordById("users", configured); err == nil {
			return configured
		}
		s.warnOnce("owner|"+configured, "consume folder: configured owner not found; using the first admin", "owner", configured)
	}
	admins, err := s.app.FindRecordsByFilter("users", "is_app_admin = true", "created", 1, 0)
	if err != nil || len(admins) == 0 {
		return ""
	}
	return admins[0].Id
}

func loadTagKeys(app core.App, ownerID string) (map[string]string, error) {
	records, err := app.FindRecordsByFilter("tags", "user = {:user}", "", 0, 0, map[string]any{"user": ownerID})
	if err != nil {
		return nil, err
	}
	keys := make(map[string]string, len(records))
	for _, record := range records {
		keys[worker.NormalizeTagKey(record.GetString("name"))] = record.Id
	}
	return keys, nil
}

// tagsFor maps the file's folders under the root to tag ids, creating the ones
// the owner does not have yet; created lists the keys of those. Matched on the
// pipeline's normalized key, so Taxes/ and taxes/ are one tag.
func (s *Scanner) tagsFor(ownerID, rel string, keys map[string]string) (ids, created []string, err error) {
	dir := filepath.Dir(rel)
	if dir == "." {
		return nil, nil, nil
	}
	for _, name := range strings.Split(dir, string(filepath.Separator)) {
		key := worker.NormalizeTagKey(name)
		if key == "" {
			continue
		}
		id, ok := keys[key]
		if !ok {
			var isNew bool
			id, isNew, err = worker.EnsureTag(s.app, ownerID, name)
			if err != nil {
				s.dropTags(created, keys)
				return nil, nil, err
			}
			keys[key] = id
			if isNew {
				created = append(created, key)
			}
		}
		ids = append(ids, id)
	}
	return ids, created, nil
}

// dropTags removes tags tagsFor made for a document that was never created.
func (s *Scanner) dropTags(created []string, keys map[string]string) {
	for _, key := range created {
		if tag, err := s.app.FindRecordById("tags", keys[key]); err == nil {
			if err := s.app.Delete(tag); err != nil {
				s.app.Logger().Warn("consume folder: remove unused tag failed", "tag", tag.Id, "error", err)
			}
		}
		delete(keys, key)
	}
}

const ledgerCollection = "ingest_files"

// loadLedger is what keep mode already consumed for ownerID: path under the
// root to the size and mtime it had.
func loadLedger(app core.App, ownerID string) (map[string]string, error) {
	records, err := app.FindRecordsByFilter(ledgerCollection, "user = {:user}", "", 0, 0, map[string]any{"user": ownerID})
	if err != nil {
		return nil, err
	}
	ledger := make(map[string]string, len(records))
	for _, record := range records {
		ledger[record.GetString("path")] = record.GetString("stamp")
	}
	return ledger, nil
}

func recordLedger(app core.App, ownerID, rel, stamp string) error {
	record, err := app.FindFirstRecordByFilter(ledgerCollection, "user = {:user} && path = {:path}",
		map[string]any{"user": ownerID, "path": rel})
	if err != nil {
		collection, err := app.FindCollectionByNameOrId(ledgerCollection)
		if err != nil {
			return err
		}
		record = core.NewRecord(collection)
		record.Set("user", ownerID)
		record.Set("path", rel)
	}
	record.Set("stamp", stamp)
	return app.Save(record)
}

// regularFile opens the file the walk saw, and only that: a symlink swapped in
// after the walk is not followed, and whatever is no longer a regular file
// within the documents.file cap is refused.
type regularFile string

func (p regularFile) Open() (io.ReadSeekCloser, error) {
	f, err := os.OpenFile(string(p), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Size() > models.MaxFileBytes) {
		err = errors.New("no longer a regular file within the document size limit")
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// roomExhausted is a limit every later file would hit too, as opposed to one
// this file alone exceeds.
func roomExhausted(err error) bool {
	var apiErr *router.ApiError
	if !errors.As(err, &apiErr) {
		return false
	}
	// Data is the client-safe rendering; the *ErrExceeded survives only in the
	// raw map the limits hook handed to NewBadRequestError.
	raw, _ := apiErr.RawData().(map[string]any)
	exceeded, ok := raw["limit"].(*limits.ErrExceeded)
	if !ok {
		return false
	}
	switch exceeded.Name {
	case limits.NameDocuments, limits.NameDocumentPages, limits.NameStorageBytes:
		return true
	}
	return false
}

// rejected is a 400 from the create hooks or PocketBase's field validation:
// the file itself was refused, so retrying it changes nothing.
func rejected(err error) bool {
	var apiErr *router.ApiError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest
}
