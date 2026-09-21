// Package dirimport turns a watched directory (INGEST_DIR) into documents: a
// cron walks it, every storable file becomes a document owned by the configured
// account, and the subfolders it sat in become its tags.
package dirimport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

type fileStamp struct {
	size  int64
	mtime time.Time
}

type Scanner struct {
	app     core.App
	rt      *config.Runtime
	dir     string
	running atomic.Bool
	// Files already known to be duplicates or refused, so keep mode does not
	// reopen and rehash them every scan. Only the scan goroutine touches it.
	seen map[string]fileStamp
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
	s := &Scanner{app: app, rt: rt, dir: dir, seen: map[string]fileStamp{}}
	app.Cron().MustAdd(cronJob, cronExpr(config.DefaultIngestDirIntervalMin), s.tick)
	rt.OnReload(func(_ core.App, snap config.Snapshot) {
		expr := cronExpr(snap.Cfg.IngestDirIntervalMin)
		app.Cron().Remove(cronJob)
		app.Cron().MustAdd(cronJob, expr, s.tick)
	})
	app.Logger().Info("consume folder registered", "dir", dir)
}

// cronExpr is every N minutes up to an hour, then every N/60 hours.
func cronExpr(minutes int) string {
	switch {
	case minutes <= 1:
		return "* * * * *"
	case minutes < 60:
		return fmt.Sprintf("*/%d * * * *", minutes)
	default:
		return fmt.Sprintf("0 */%d * * *", min(minutes/60, 23))
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

	ownerID := resolveOwner(s.app, cfg.IngestDirOwner)
	if ownerID == "" {
		return res
	}
	tags, err := loadTagKeys(s.app, ownerID)
	if err != nil {
		logger.Warn("consume folder: load tags failed", "error", err)
		return res
	}

	removeFailed := false
	remove := func(path string) {
		if !cfg.IngestDirDeleteOriginal {
			return
		}
		if err := os.Remove(path); err != nil && !removeFailed {
			removeFailed = true
			logger.Warn("consume folder: delete original failed; keeping files", "path", path, "error", err)
		}
	}

	stop := errors.New("stop")
	walkErr := filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == s.dir {
				return err
			}
			logger.Warn("consume folder: unreadable entry", "path", path, "error", err)
			return nil
		}
		if path != s.dir && strings.HasPrefix(d.Name(), ".") {
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
		stamp := fileStamp{size: info.Size(), mtime: info.ModTime()}
		if s.seen[path] == stamp {
			return nil
		}

		checksum, err := hashFile(path)
		if err != nil {
			logger.Warn("consume folder: hash failed", "path", path, "error", err)
			return nil
		}
		existing, err := duplicates.FindByChecksum(s.app, ownerID, checksum, "")
		if err != nil {
			logger.Warn("consume folder: duplicate lookup failed", "path", path, "error", err)
			return nil
		}
		if existing != nil {
			res.Skipped++
			s.seen[path] = stamp
			remove(path)
			return nil
		}

		tagIDs, err := s.tagsFor(ownerID, path, tags)
		if err != nil {
			logger.Warn("consume folder: tag lookup failed", "path", path, "error", err)
			return nil
		}
		err = createDocument(s.app, ownerID, path, checksum, tagIDs)
		var dup *duplicates.ErrDuplicate
		switch {
		case err == nil:
			res.Created++
			delete(s.seen, path)
			remove(path)
		case errors.As(err, &dup):
			res.Skipped++
			s.seen[path] = stamp
			remove(path)
		case roomExhausted(err):
			logger.Warn("consume folder: limit reached, stopping this scan", "error", err)
			return stop
		default:
			res.Failed++
			s.seen[path] = stamp
			logger.Warn("consume folder: import failed", "path", path, "error", err)
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, stop) {
		logger.Warn("consume folder: walk failed", "dir", s.dir, "error", walkErr)
	}
	return res
}

// resolveOwner is the configured account, else the oldest admin's paired users
// row. Empty before setup has run, which is not an error.
func resolveOwner(app core.App, configured string) string {
	if configured != "" {
		if _, err := app.FindRecordById("users", configured); err == nil {
			return configured
		}
		app.Logger().Warn("consume folder: configured owner not found; using the first admin", "owner", configured)
	}
	admins, err := app.FindRecordsByFilter("users", "is_app_admin = true", "created", 1, 0)
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
// the owner does not have yet. Matched on the pipeline's normalized key, so
// Taxes/ and taxes/ are one tag.
func (s *Scanner) tagsFor(ownerID, path string, keys map[string]string) ([]string, error) {
	rel, err := filepath.Rel(s.dir, filepath.Dir(path))
	if err != nil || rel == "." {
		return nil, err
	}
	var ids []string
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		key := worker.NormalizeTagKey(name)
		if key == "" {
			continue
		}
		id, ok := keys[key]
		if !ok {
			id, _, err = worker.EnsureTag(s.app, ownerID, name)
			if err != nil {
				return nil, err
			}
			keys[key] = id
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return duplicates.SHA256Reader(f)
}

func createDocument(app core.App, ownerID, path, checksum string, tagIDs []string) error {
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fsFile, err := filesystem.NewFileFromBytes(data, filepath.Base(path))
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("user", ownerID)
	record.Set("file", fsFile)
	record.Set("checksum", checksum)
	record.Set("processing_status", models.DocStatusPending)
	if len(tagIDs) > 0 {
		record.Set("tags", tagIDs)
	}
	return duplicates.NormalizeSaveError(app, record, app.Save(record))
}

// roomExhausted is a limit every later file would hit too, as opposed to one
// this file alone exceeds.
func roomExhausted(err error) bool {
	var apiErr *router.ApiError
	if !errors.As(err, &apiErr) {
		return false
	}
	exceeded, ok := apiErr.Data["limit"].(*limits.ErrExceeded)
	if !ok {
		return false
	}
	switch exceeded.Name {
	case limits.NameDocuments, limits.NameDocumentPages, limits.NameStorageBytes:
		return true
	}
	return false
}
