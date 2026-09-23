// Package imapimport turns an IMAP mailbox (INGEST_IMAP_ENABLED) into
// documents: a cron reads the configured folder, and every storable attachment
// becomes a document owned by the consume folder's owner.
package imapimport

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/dirimport"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/inflight"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/zipimport"
)

const cronJob = "imap_ingest"

// A hung server would otherwise hold the scan, and with it every later tick.
const sessionTimeout = 15 * time.Minute

// Base64 grows a part by a third, plus line breaks; a part the server reports
// above this cannot decode within the document cap, so it is never fetched.
const maxEncodedBytes = models.MaxFileBytes/3*4 + models.MaxFileBytes/38 + 1024

type Result struct {
	Created int
	// Skipped attachments are already in the library (same checksum).
	Skipped int
	Failed  int
}

type Scanner struct {
	app     core.App
	rt      *config.Runtime
	dial    func(cfg config.Config) (*imapclient.Client, error)
	running atomic.Bool
	// Messages this process already decided about, by UID. Valid for one
	// owner, mode, mailbox and UIDVALIDITY: seenKey.
	seen    map[imap.UID]bool
	seenKey string
	// Delete and move mode: consumed messages waiting for encryption at rest to
	// seal their documents, by when the last one was saved.
	pending map[imap.UID]time.Time
	warned  map[string]bool
}

func newScanner(app core.App, rt *config.Runtime) *Scanner {
	return &Scanner{app: app, rt: rt, dial: dial, seen: map[imap.UID]bool{},
		pending: map[imap.UID]time.Time{}, warned: map[string]bool{}}
}

// Register schedules the scan on the consume folder's interval, re-added with
// a new expression on every settings reload.
func Register(app core.App, rt *config.Runtime) {
	s := newScanner(app, rt)
	app.Cron().MustAdd(cronJob, config.IngestCronExpr(config.DefaultIngestDirIntervalMin), s.tick)
	rt.OnReload(func(_ core.App, snap config.Snapshot) {
		app.Cron().Remove(cronJob)
		app.Cron().MustAdd(cronJob, config.IngestCronExpr(snap.Cfg.IngestDirIntervalMin), s.tick)
	})
	app.Logger().Info("imap ingest registered")
}

func (s *Scanner) tick() {
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	defer s.running.Store(false)
	defer inflight.Begin()()

	res := s.Scan(s.rt.Snapshot().Cfg)
	if res.Created+res.Skipped+res.Failed > 0 {
		s.app.Logger().Info("imap ingest scanned", "created", res.Created, "skipped", res.Skipped, "failed", res.Failed)
	}
}

func address(cfg config.Config) string {
	if _, _, err := net.SplitHostPort(cfg.IMAPHost); err == nil {
		return cfg.IMAPHost
	}
	port := "993"
	if cfg.IMAPSecurity == config.IMAPSecuritySTARTTLS {
		port = "143"
	}
	return net.JoinHostPort(cfg.IMAPHost, port)
}

func dial(cfg config.Config) (*imapclient.Client, error) {
	opts := &imapclient.Options{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if cfg.IMAPSecurity == config.IMAPSecuritySTARTTLS {
		return imapclient.DialStartTLS(address(cfg), opts)
	}
	return imapclient.DialTLS(address(cfg), opts)
}

type part struct {
	path     []int
	name     string
	encoding string
	size     uint32
}

type outcome int

const (
	// consumed: every attachment is now a document, or already was one.
	consumed outcome = iota
	// refused: some attachment was rejected; the message stays in the mailbox.
	refused
	// retry: a limit or a transient error; nothing after it is read this scan.
	retry
)

// Scan reads the mailbox once.
func (s *Scanner) Scan(cfg config.Config) Result {
	var res Result
	logger := s.app.Logger()
	if cfg.IMAPHost == "" || cfg.IMAPUsername == "" {
		return res
	}
	ownerID, missing := dirimport.ResolveOwner(s.app, cfg.IngestDirOwner)
	if missing {
		s.warnOnce("owner|"+cfg.IngestDirOwner, "imap ingest: configured owner not found; using the first admin", "owner", cfg.IngestDirOwner)
	}
	if ownerID == "" {
		return res
	}
	mode := cfg.IMAPAfterConsume

	c, err := s.dial(cfg)
	if err != nil {
		s.warnOnce("dial|"+err.Error(), "imap ingest: connect failed", "host", cfg.IMAPHost, "error", err)
		return res
	}
	defer c.Close()
	timer := time.AfterFunc(sessionTimeout, func() { c.Close() })
	defer timer.Stop()

	if err := c.Login(cfg.IMAPUsername, cfg.IMAPPassword).Wait(); err != nil {
		s.warnOnce("login|"+err.Error(), "imap ingest: login failed", "host", cfg.IMAPHost, "user", cfg.IMAPUsername, "error", err)
		return res
	}
	defer c.Logout()
	if mode == config.IMAPMove {
		// Already existing is the usual answer, and a real failure shows on the move.
		_ = c.Create(cfg.IMAPMoveFolder, nil).Wait()
	}
	sel, err := c.Select(cfg.IMAPFolder, &imap.SelectOptions{ReadOnly: mode == config.IMAPKeep}).Wait()
	if err != nil {
		s.warnOnce("select|"+cfg.IMAPFolder, "imap ingest: open folder failed", "folder", cfg.IMAPFolder, "error", err)
		return res
	}
	mailbox := fmt.Sprintf("imap://%s@%s/%s", cfg.IMAPUsername, cfg.IMAPHost, cfg.IMAPFolder)
	if key := fmt.Sprintf("%s|%s|%s|%d", ownerID, mode, mailbox, sel.UIDValidity); key != s.seenKey {
		s.seen = map[imap.UID]bool{}
		s.pending = map[imap.UID]time.Time{}
		s.warned = map[string]bool{}
		s.seenKey = key
	}
	if mode != config.IMAPKeep {
		defer s.settle(c, cfg)
	}

	var last imap.UID
	if mode == config.IMAPKeep {
		last = s.loadMark(ownerID, mailbox, sel.UIDValidity)
		mark := last
		defer func() {
			if last == mark {
				return
			}
			stamp := fmt.Sprintf("%d:%d", sel.UIDValidity, last)
			if err := dirimport.RecordLedger(s.app, ownerID, mailbox, stamp); err != nil {
				logger.Warn("imap ingest: ledger write failed", "error", err)
			}
		}()
	}
	found, err := c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		logger.Warn("imap ingest: search failed", "folder", cfg.IMAPFolder, "error", err)
		return res
	}
	var uids []imap.UID
	for _, uid := range found.AllUIDs() {
		if uid > last && !s.seen[uid] {
			uids = append(uids, uid)
		}
	}
	if len(uids) == 0 {
		return res
	}
	msgs, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:           true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		logger.Warn("imap ingest: fetch failed", "folder", cfg.IMAPFolder, "error", err)
		return res
	}
	slices.SortFunc(msgs, func(a, b *imapclient.FetchMessageBuffer) int { return int(a.UID) - int(b.UID) })

	collection, err := s.app.FindCollectionByNameOrId("documents")
	if err != nil {
		logger.Warn("imap ingest: documents collection", "error", err)
		return res
	}
	for _, msg := range msgs {
		parts := attachments(msg.BodyStructure)
		got := consumed
		if len(parts) > 0 {
			got = s.consume(c, collection, ownerID, msg.UID, parts, &res)
		}
		if got == retry {
			// ponytail: one transient failure holds every later message until it
			// clears; per-message ledger rows if a stuck message ever blocks a mailbox.
			break
		}
		s.seen[msg.UID] = true
		last = max(last, msg.UID)
		if mode != config.IMAPKeep && got == consumed && len(parts) > 0 {
			s.pending[msg.UID] = time.Now()
		}
	}
	return res
}

func (s *Scanner) consume(c *imapclient.Client, collection *core.Collection, ownerID string, uid imap.UID, parts []part, res *Result) outcome {
	logger := s.app.Logger()
	var fetchable []part
	sections := make([]*imap.FetchItemBodySection, 0, len(parts))
	got := consumed
	for _, p := range parts {
		if int64(p.size) > maxEncodedBytes {
			res.Failed++
			got = refused
			logger.Warn("imap ingest: attachment exceeds the document size limit", "uid", uid, "name", p.name, "size", p.size)
			continue
		}
		fetchable = append(fetchable, p)
		sections = append(sections, &imap.FetchItemBodySection{Part: p.path, Peek: true})
	}
	if len(fetchable) == 0 {
		return got
	}
	msgs, err := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{UID: true, BodySection: sections}).Collect()
	if err != nil || len(msgs) == 0 {
		s.warnOnce(fmt.Sprintf("fetch|%d", uid), "imap ingest: fetch attachments failed", "uid", uid, "error", err)
		return retry
	}
	for i, p := range fetchable {
		data, err := decode(msgs[0].FindBodySection(sections[i]), p.encoding)
		if err == nil && (len(data) == 0 || int64(len(data)) > models.MaxFileBytes) {
			err = errors.New("empty or above the document size limit")
		}
		if err != nil {
			res.Failed++
			got = refused
			logger.Warn("imap ingest: unreadable attachment", "uid", uid, "name", p.name, "error", err)
			continue
		}
		file, err := filesystem.NewFileFromBytes(data, p.name)
		if err != nil {
			res.Failed++
			got = refused
			continue
		}
		err = zipimport.CreateDocument(s.app, collection, ownerID, file, nil)
		var dup *duplicates.ErrDuplicate
		switch {
		case err == nil:
			res.Created++
		case errors.As(err, &dup):
			res.Skipped++
		case dirimport.RoomExhausted(err):
			logger.Warn("imap ingest: limit reached, stopping this scan", "error", err)
			return retry
		case dirimport.Rejected(err):
			res.Failed++
			got = refused
			s.warnOnce(fmt.Sprintf("rejected|%d|%s", uid, p.name), "imap ingest: attachment refused", "uid", uid, "name", p.name, "error", err)
		default:
			res.Failed++
			s.warnOnce(fmt.Sprintf("import|%d|%s", uid, p.name), "imap ingest: import failed", "uid", uid, "name", p.name, "error", err)
			return retry
		}
	}
	return got
}

// attachments are the single parts with a filename the documents collection
// can store; a mail body without one is not a document.
func attachments(bs imap.BodyStructure) []part {
	if bs == nil {
		return nil
	}
	var out []part
	bs.Walk(func(path []int, node imap.BodyStructure) bool {
		single, ok := node.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		name := single.Filename()
		if name == "" || !zipimport.Storable(filepath.Ext(name)) {
			return true
		}
		out = append(out, part{path: slices.Clone(path), name: name, encoding: single.Encoding, size: single.Size})
		return false
	})
	return out
}

func decode(raw []byte, encoding string) ([]byte, error) {
	var r io.Reader = bytes.NewReader(raw)
	switch strings.ToLower(encoding) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	return io.ReadAll(io.LimitReader(r, models.MaxFileBytes+1))
}

// settle deletes or moves the consumed messages whose documents a hard kill
// can no longer lose. A failure keeps them pending for the next scan.
func (s *Scanner) settle(c *imapclient.Client, cfg config.Config) {
	var uids []imap.UID
	for uid, saved := range s.pending {
		if dirimport.Durable(s.app, saved) {
			uids = append(uids, uid)
		}
	}
	if len(uids) == 0 {
		return
	}
	set := imap.UIDSetNum(uids...)
	var err error
	if cfg.IMAPAfterConsume == config.IMAPMove {
		_, err = c.Move(set, cfg.IMAPMoveFolder).Wait()
	} else {
		err = c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
		if err == nil && c.Caps().Has(imap.CapUIDPlus) {
			err = c.UIDExpunge(set).Close()
		} else if err == nil {
			err = c.Expunge().Close()
		}
	}
	if err != nil {
		s.warnOnce("settle|"+err.Error(), "imap ingest: removing consumed messages failed", "mode", cfg.IMAPAfterConsume, "error", err)
		return
	}
	for _, uid := range uids {
		delete(s.pending, uid)
	}
}

// loadMark is the highest UID keep mode consumed from mailbox under the
// current UIDVALIDITY; a server that renumbered starts over, and the checksum
// check skips what was already imported.
func (s *Scanner) loadMark(ownerID, mailbox string, validity uint32) imap.UID {
	record, err := s.app.FindFirstRecordByFilter(dirimport.LedgerCollection, "user = {:user} && path = {:path}",
		map[string]any{"user": ownerID, "path": mailbox})
	if err != nil {
		return 0
	}
	v, uid, ok := strings.Cut(record.GetString("stamp"), ":")
	if !ok || v != strconv.FormatUint(uint64(validity), 10) {
		return 0
	}
	n, _ := strconv.ParseUint(uid, 10, 32)
	return imap.UID(n)
}

func (s *Scanner) warnOnce(key, msg string, args ...any) {
	if s.warned[key] {
		return
	}
	s.warned[key] = true
	s.app.Logger().Warn(msg, args...)
}
