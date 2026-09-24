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
	"sync"
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

// Messages are fetched this many at a time: a long UID list is one command
// line, which some servers cap.
const fetchBatch = 200

// A message failing this many scans in a row is given up on, so it cannot hold
// back the mail behind it; Management's backfill can still import it.
const maxAttempts = 3

// The since point is this host's clock and INTERNALDATE the server's; mail
// arriving just after a save must not be lost to a server running behind.
const clockSkew = 10 * time.Minute

type Result struct {
	Created int `json:"created"`
	// Skipped attachments are already in the library (same checksum).
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// Backfill is the last Management range scan, as the page polls it.
type Backfill struct {
	Running bool   `json:"running"`
	From    string `json:"from"`
	To      string `json:"to"`
	Result
	Error string `json:"error"`
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
	// Scans in a row that failed on a message, for maxAttempts.
	attempts map[imap.UID]int
	warned   map[string]bool

	mu       sync.Mutex
	backfill Backfill
}

var (
	ErrBusy          = errors.New("a mailbox scan is already running")
	ErrNotConfigured = errors.New("no mailbox is configured")
	ErrNoOwner       = errors.New("no account to own the documents yet")
)

func New(app core.App, rt *config.Runtime) *Scanner {
	return &Scanner{app: app, rt: rt, dial: dial, seen: map[imap.UID]bool{},
		pending: map[imap.UID]time.Time{}, attempts: map[imap.UID]int{}, warned: map[string]bool{}}
}

// Register schedules the scan on the consume folder's interval, re-added with
// a new expression on every settings reload.
func (s *Scanner) Register() {
	s.app.Cron().MustAdd(cronJob, config.IngestCronExpr(config.DefaultIngestDirIntervalMin), s.tick)
	s.rt.OnReload(func(_ core.App, snap config.Snapshot) {
		s.app.Cron().Remove(cronJob)
		s.app.Cron().MustAdd(cronJob, config.IngestCronExpr(snap.Cfg.IngestDirIntervalMin), s.tick)
	})
	s.app.Logger().Info("imap ingest registered")
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
	// retry: a transient error on this message; maxAttempts bounds it.
	retry
	// stop: an instance limit every later message would hit too.
	stop
)

// Scan reads the mailbox once.
func (s *Scanner) Scan(cfg config.Config) Result {
	var res Result
	logger := s.app.Logger()
	if cfg.IMAPHost == "" || cfg.IMAPUsername == "" {
		return res
	}
	ownerID := s.owner(cfg)
	if ownerID == "" {
		return res
	}
	mode := cfg.IMAPAfterConsume

	c, sel, done, err := s.connect(cfg, mode == config.IMAPKeep)
	if err != nil {
		s.warnOnce("connect|"+err.Error(), "imap ingest: "+err.Error())
		return res
	}
	defer done()
	if mode == config.IMAPMove {
		// Already existing is the usual answer, and a real failure shows on the move.
		_ = c.Create(cfg.IMAPMoveFolder, nil).Wait()
	}
	mailbox := fmt.Sprintf("imap://%s@%s/%s", cfg.IMAPUsername, cfg.IMAPHost, cfg.IMAPFolder)
	if key := fmt.Sprintf("%s|%s|%s|%d", ownerID, mode, mailbox, sel.UIDValidity); key != s.seenKey {
		s.seen = map[imap.UID]bool{}
		s.pending = map[imap.UID]time.Time{}
		s.attempts = map[imap.UID]int{}
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
	since := cfg.IMAPSince
	if !since.IsZero() {
		since = since.Add(-clockSkew)
	}
	// SINCE is a date; the exact cut is the INTERNALDATE check below.
	found, err := c.UIDSearch(liveMail(imap.SearchCriteria{Since: since}), nil).Wait()
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
	for batch := range slices.Chunk(uids, fetchBatch) {
		msgs, collection, err := s.structures(c, batch)
		if err != nil {
			logger.Warn("imap ingest: "+err.Error(), "folder", cfg.IMAPFolder)
			return res
		}
		for _, msg := range msgs {
			var parts []part
			if !msg.InternalDate.Before(since) {
				parts = attachments(msg.BodyStructure)
			}
			got := consumed
			if len(parts) > 0 {
				got = s.consume(c, collection, ownerID, msg.UID, parts, &res)
			}
			if got == retry {
				if s.attempts[msg.UID]++; s.attempts[msg.UID] < maxAttempts {
					return res
				}
				logger.Error("imap ingest: giving up on a message; Management -> Mailbox can import it later",
					"uid", msg.UID, "attempts", maxAttempts)
				got = refused
			}
			if got == stop {
				return res
			}
			delete(s.attempts, msg.UID)
			s.seen[msg.UID] = true
			last = max(last, msg.UID)
			if mode != config.IMAPKeep && got == consumed && len(parts) > 0 {
				s.pending[msg.UID] = time.Now()
			}
		}
	}
	return res
}

// liveMail leaves out what a mail client has already deleted but not yet
// expunged, and what a server without UIDPLUS keeps flagged after a move.
func liveMail(criteria imap.SearchCriteria) *imap.SearchCriteria {
	criteria.NotFlag = []imap.Flag{imap.FlagDeleted}
	return &criteria
}

// StartBackfill imports, in the background, the attachments of mail received
// from from up to, not including, to; BackfillStatus reports on it. The since
// point, the ledger and the after-import action do not apply, and the checksum
// check skips what the library already has. It holds the scan lock, so a
// scheduled scan cannot move or delete in the folder it is reading.
func (s *Scanner) StartBackfill(cfg config.Config, from, to time.Time) error {
	if cfg.IMAPHost == "" || cfg.IMAPUsername == "" {
		return ErrNotConfigured
	}
	if !s.running.CompareAndSwap(false, true) {
		return ErrBusy
	}
	s.setBackfill(Backfill{Running: true, From: from.Format(time.DateOnly), To: to.AddDate(0, 0, -1).Format(time.DateOnly)})
	go func() {
		defer s.running.Store(false)
		defer inflight.Begin()()
		res, err := s.scanRange(cfg, from, to)
		state := s.BackfillStatus()
		state.Running, state.Result = false, res
		if err != nil {
			state.Error = err.Error()
			s.app.Logger().Warn("imap ingest: backfill failed", "error", err)
		}
		s.setBackfill(state)
	}()
	return nil
}

func (s *Scanner) BackfillStatus() Backfill {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backfill
}

func (s *Scanner) setBackfill(state Backfill) {
	s.mu.Lock()
	s.backfill = state
	s.mu.Unlock()
}

// scanRange is StartBackfill's work, run under the scan lock. A message that
// fails is counted and passed over; only an instance limit stops it.
func (s *Scanner) scanRange(cfg config.Config, from, to time.Time) (Result, error) {
	var res Result
	ownerID := s.owner(cfg)
	if ownerID == "" {
		return res, ErrNoOwner
	}
	c, _, done, err := s.connect(cfg, true)
	if err != nil {
		return res, err
	}
	defer done()
	found, err := c.UIDSearch(liveMail(imap.SearchCriteria{Since: from, Before: to}), nil).Wait()
	if err != nil {
		return res, fmt.Errorf("search %s: %w", cfg.IMAPFolder, err)
	}
	for batch := range slices.Chunk(found.AllUIDs(), fetchBatch) {
		msgs, collection, err := s.structures(c, batch)
		if err != nil {
			return res, err
		}
		for _, msg := range msgs {
			parts := attachments(msg.BodyStructure)
			if len(parts) > 0 && s.consume(c, collection, ownerID, msg.UID, parts, &res) == stop {
				return res, errors.New("stopped at an instance limit")
			}
		}
	}
	return res, nil
}

func (s *Scanner) owner(cfg config.Config) string {
	ownerID, missing := dirimport.ResolveOwner(s.app, cfg.IngestDirOwner)
	if missing {
		s.warnOnce("owner|"+cfg.IngestDirOwner, "imap ingest: configured owner not found; using the first admin", "owner", cfg.IngestDirOwner)
	}
	return ownerID
}

// connect logs in and opens the folder; done ends the session.
func (s *Scanner) connect(cfg config.Config, readOnly bool) (*imapclient.Client, *imap.SelectData, func(), error) {
	c, err := s.dial(cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect to %s: %w", cfg.IMAPHost, err)
	}
	timer := time.AfterFunc(sessionTimeout, func() { c.Close() })
	done := func() {
		timer.Stop()
		_ = c.Logout().Wait()
		c.Close()
	}
	if err := c.Login(cfg.IMAPUsername, cfg.IMAPPassword).Wait(); err != nil {
		done()
		return nil, nil, nil, fmt.Errorf("log in to %s as %s: %w", cfg.IMAPHost, cfg.IMAPUsername, err)
	}
	sel, err := c.Select(cfg.IMAPFolder, &imap.SelectOptions{ReadOnly: readOnly}).Wait()
	if err != nil {
		done()
		return nil, nil, nil, fmt.Errorf("open folder %s: %w", cfg.IMAPFolder, err)
	}
	return c, sel, done, nil
}

// structures fetches what a scan decides on for uids, oldest first.
func (s *Scanner) structures(c *imapclient.Client, uids []imap.UID) ([]*imapclient.FetchMessageBuffer, *core.Collection, error) {
	if len(uids) == 0 {
		return nil, nil, nil
	}
	msgs, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:           true,
		InternalDate:  true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return nil, nil, fmt.Errorf("fetch: %w", err)
	}
	slices.SortFunc(msgs, func(a, b *imapclient.FetchMessageBuffer) int { return int(a.UID) - int(b.UID) })
	collection, err := s.app.FindCollectionByNameOrId("documents")
	if err != nil {
		return nil, nil, fmt.Errorf("documents collection: %w", err)
	}
	return msgs, collection, nil
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
		res.Failed++
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
			return stop
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
// can store, including those inside a forwarded message; a mail body without
// one is not a document.
func attachments(bs imap.BodyStructure) []part {
	return collect(bs, nil, nil)
}

// collect walks by IMAP section number. imap.BodyStructure.Walk stops at a
// message/rfc822 part, whose own parts are numbered under it (2.1, 2.2, or
// 2.1 for a single-part message).
func collect(bs imap.BodyStructure, path []int, out []part) []part {
	switch node := bs.(type) {
	case *imap.BodyStructureMultiPart:
		for i, child := range node.Children {
			out = collect(child, append(slices.Clone(path), i+1), out)
		}
	case *imap.BodyStructureSinglePart:
		if len(path) == 0 {
			path = []int{1}
		}
		if name := node.Filename(); name != "" && zipimport.Storable(filepath.Ext(name)) {
			return append(out, part{path: path, name: name, encoding: node.Encoding, size: node.Size})
		}
		if node.MessageRFC822 == nil || node.MessageRFC822.BodyStructure == nil {
			return out
		}
		inner := node.MessageRFC822.BodyStructure
		if _, multi := inner.(*imap.BodyStructureMultiPart); multi {
			return collect(inner, path, out)
		}
		return collect(inner, append(slices.Clone(path), 1), out)
	}
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
	caps := c.Caps()
	flag := func() error {
		return c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
	}
	var err error
	switch {
	case cfg.IMAPAfterConsume == config.IMAPMove && (caps.Has(imap.CapMove) || caps.Has(imap.CapUIDPlus)):
		_, err = c.Move(set, cfg.IMAPMoveFolder).Wait()
	case cfg.IMAPAfterConsume == config.IMAPMove:
		// Without MOVE or UIDPLUS, go-imap's fallback is a bare EXPUNGE, which
		// also purges whatever a mail client flagged \Deleted. Copy and flag
		// instead; the next expunge the user's client runs finishes the move.
		if _, err = c.Copy(set, cfg.IMAPMoveFolder).Wait(); err == nil {
			err = flag()
		}
		s.warnOnce("nouidplus", "imap ingest: server lacks MOVE and UIDPLUS; moved messages stay flagged \\Deleted in the source folder")
	case caps.Has(imap.CapUIDPlus):
		if err = flag(); err == nil {
			err = c.UIDExpunge(set).Close()
		}
	default:
		err = flag()
		s.warnOnce("nouidplus", "imap ingest: server lacks UIDPLUS; consumed messages are flagged \\Deleted, not expunged")
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
