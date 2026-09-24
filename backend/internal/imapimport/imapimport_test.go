package imapimport

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/inflight"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/testpb"
	"lemmary/backend/internal/zipimport"
	_ "lemmary/backend/migrations"
)

func openApp(t *testing.T, lim limits.Limits) core.App {
	t.Helper()
	app := testpb.Open(t)
	limits.Register(app, lim)
	app.OnRecordCreate("documents").BindFunc(func(e *core.RecordEvent) error {
		if err := duplicates.AssignChecksumFromUpload(e.App, e.Record); err != nil {
			return err
		}
		return e.Next()
	})
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	admin := core.NewRecord(users)
	admin.Set("email", "admin@example.com")
	admin.Set("is_app_admin", true)
	admin.SetPassword("test-password-123")
	if err := app.Save(admin); err != nil {
		t.Fatal(err)
	}
	return app
}

type mailbox struct {
	user *imapmemserver.User
	addr string
}

// startServer speaks IMAP4rev2 (MOVE, UIDPLUS) unless caps names others.
func startServer(t *testing.T, caps ...imap.Cap) *mailbox {
	t.Helper()
	capSet := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}}
	if len(caps) > 0 {
		capSet = imap.CapSet{}
		for _, c := range caps {
			capSet[c] = struct{}{}
		}
	}
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("docs", "pw")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(user)
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps:         capSet,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)
	t.Cleanup(func() { server.Close() })
	return &mailbox{user: user, addr: ln.Addr().String()}
}

func (m *mailbox) scanner(app core.App) *Scanner {
	s := New(app, nil)
	s.dial = func(config.Config) (*imapclient.Client, error) { return imapclient.DialInsecure(m.addr, nil) }
	return s
}

func (m *mailbox) deliver(t *testing.T, folder string, parts ...string) {
	t.Helper()
	m.deliverAt(t, folder, time.Time{}, parts...)
}

// deliverAt stamps the INTERNALDATE; zero is now.
func (m *mailbox) deliverAt(t *testing.T, folder string, received time.Time, parts ...string) {
	t.Helper()
	m.append(t, folder, &imap.AppendOptions{Time: received}, parts...)
}

func (m *mailbox) append(t *testing.T, folder string, opts *imap.AppendOptions, parts ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("From: sender@example.com\nTo: docs@example.com\nSubject: scan\nMIME-Version: 1.0\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"b\"\n\n--b\nContent-Type: text/plain\n\nsee attached\n")
	for _, p := range parts {
		b.WriteString("--b\n" + p)
	}
	b.WriteString("--b--\n")
	raw := []byte(strings.ReplaceAll(b.String(), "\n", "\r\n"))
	if _, err := m.user.Append(folder, bytes.NewReader(raw), opts); err != nil {
		t.Fatal(err)
	}
}

func (m *mailbox) count(t *testing.T, folder string) uint32 {
	t.Helper()
	status, err := m.user.Status(folder, &imap.StatusOptions{NumMessages: true})
	if err != nil {
		t.Fatal(err)
	}
	return *status.NumMessages
}

func base64Part(name, content string) string {
	return fmt.Sprintf("Content-Type: text/plain; name=%q\nContent-Disposition: attachment; filename=%q\nContent-Transfer-Encoding: base64\n\n%s\n",
		name, name, base64.StdEncoding.EncodeToString([]byte(content)))
}

func qpPart(name, content string) string {
	return fmt.Sprintf("Content-Type: text/plain\nContent-Disposition: attachment; filename=%q\nContent-Transfer-Encoding: quoted-printable\n\n%s\n",
		name, content)
}

func cfg(after string) config.Config {
	return config.Config{IMAPHost: "mail.test", IMAPUsername: "docs", IMAPPassword: "pw",
		IMAPFolder: "INBOX", IMAPAfterConsume: after, IMAPMoveFolder: "Done"}
}

func TestKeepModeImportsEachAttachmentOnceAcrossRestarts(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliver(t, "INBOX", base64Part("invoice.txt", "first document"), qpPart("notes.txt", "caf=C3=A9 receipt"))
	m.deliver(t, "INBOX")
	m.deliver(t, "INBOX", base64Part("setup.exe", "not storable"))

	if res := m.scanner(app).Scan(cfg(config.IMAPKeep)); res != (Result{Created: 2}) {
		t.Fatalf("first scan = %+v, want the two attachments", res)
	}
	docs, err := app.FindRecordsByFilter("documents", "", "created", 0, 0)
	if err != nil || len(docs) != 2 {
		t.Fatalf("documents = %d, %v", len(docs), err)
	}

	// A new process has no seen map; the ledger is what stops a re-import, even
	// after the documents are gone.
	for _, doc := range docs {
		if err := app.Delete(doc); err != nil {
			t.Fatal(err)
		}
	}
	if res := m.scanner(app).Scan(cfg(config.IMAPKeep)); res != (Result{}) {
		t.Fatalf("scan after restart = %+v, want nothing", res)
	}

	m.deliver(t, "INBOX", base64Part("later.txt", "third document"))
	if res := m.scanner(app).Scan(cfg(config.IMAPKeep)); res != (Result{Created: 1}) {
		t.Fatalf("scan after a new mail = %+v, want just it", res)
	}
	if got := m.count(t, "INBOX"); got != 4 {
		t.Fatalf("INBOX holds %d messages, keep mode must leave all 4", got)
	}
}

func TestDecodesQuotedPrintableAttachments(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliver(t, "INBOX", qpPart("notes.txt", "caf=C3=A9 receipt"))
	if res := m.scanner(app).Scan(cfg(config.IMAPKeep)); res.Created != 1 {
		t.Fatalf("scan = %+v", res)
	}
	doc, err := app.FindFirstRecordByFilter("documents", "")
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := app.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	r, err := fsys.GetReader(doc.BaseFilesPath() + "/" + doc.GetString("file"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var got bytes.Buffer
	got.ReadFrom(r)
	if got.String() != "café receipt" {
		t.Fatalf("stored %q, want the decoded text", got.String())
	}
}

func TestDeleteModeExpungesOnlyConsumedMessages(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliver(t, "INBOX", base64Part("a.txt", "first document"))
	m.deliver(t, "INBOX", base64Part("dup.txt", "first document"))
	m.deliver(t, "INBOX")

	s := m.scanner(app)
	if res := s.Scan(cfg(config.IMAPDelete)); res != (Result{Created: 1, Skipped: 1}) {
		t.Fatalf("scan = %+v, want one created and one duplicate", res)
	}
	if got := m.count(t, "INBOX"); got != 1 {
		t.Fatalf("INBOX holds %d, want only the mail without an attachment", got)
	}
	if res := s.Scan(cfg(config.IMAPDelete)); res != (Result{}) {
		t.Fatalf("second scan = %+v, want nothing", res)
	}
}

func TestMoveModeMovesConsumedMessagesToTheTarget(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliver(t, "INBOX", base64Part("a.txt", "first document"))

	if res := m.scanner(app).Scan(cfg(config.IMAPMove)); res.Created != 1 {
		t.Fatalf("scan = %+v", res)
	}
	if m.count(t, "INBOX") != 0 || m.count(t, "Done") != 1 {
		t.Fatalf("INBOX %d, Done %d; want the message moved", m.count(t, "INBOX"), m.count(t, "Done"))
	}
}

func TestRefusedAttachmentKeepsTheMessage(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliver(t, "INBOX", base64Part("bad.txt", ""))

	s := m.scanner(app)
	if res := s.Scan(cfg(config.IMAPDelete)); res != (Result{Failed: 1}) {
		t.Fatalf("scan = %+v, want the empty attachment refused", res)
	}
	if got := m.count(t, "INBOX"); got != 1 {
		t.Fatalf("a refused attachment's message was removed (INBOX %d)", got)
	}
	if res := s.Scan(cfg(config.IMAPDelete)); res != (Result{}) {
		t.Fatalf("second scan = %+v, want the refusal remembered", res)
	}
}

func TestScanStopsAtTheInstanceLimitAndRetriesLater(t *testing.T) {
	app := openApp(t, limits.Limits{Documents: limits.Of(1)})
	m := startServer(t)
	m.deliver(t, "INBOX", base64Part("a.txt", "first"))
	m.deliver(t, "INBOX", base64Part("b.txt", "second"))

	s := m.scanner(app)
	if res := s.Scan(cfg(config.IMAPKeep)); res.Created != 1 || res.Failed != 0 {
		t.Fatalf("scan under a one-document cap = %+v, want 1 created and a stop", res)
	}
	if len(s.seen) != 1 {
		t.Fatalf("seen = %v; a room limit must not mark the message as handled", s.seen)
	}
	if got := s.loadMark(adminID(t, app), "imap://docs@mail.test/INBOX", uidValidity(t, m)); got != 1 {
		t.Fatalf("ledger mark = %d, want the first message only", got)
	}
}

func TestDeleteModeWaitsUntilTheDocumentIsSealed(t *testing.T) {
	app := openApp(t, limits.Limits{})
	seal := inflight.NewSeal()
	app.Store().Set(inflight.SealStoreKey, seal)
	app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		seal.Wrote()
		return e.Next()
	})
	m := startServer(t)
	m.deliver(t, "INBOX", base64Part("a.txt", "only copy"))

	s := m.scanner(app)
	if res := s.Scan(cfg(config.IMAPDelete)); res.Created != 1 {
		t.Fatalf("scan = %+v", res)
	}
	if got := m.count(t, "INBOX"); got != 1 {
		t.Fatal("message removed before its document was sealed")
	}
	seal.Sealed(time.Now().UnixNano())
	if res := s.Scan(cfg(config.IMAPDelete)); res != (Result{}) {
		t.Fatalf("second scan = %+v, want the pending message not re-imported", res)
	}
	if got := m.count(t, "INBOX"); got != 0 {
		t.Fatalf("INBOX holds %d once sealed, want 0", got)
	}
}

func TestScanDoesNothingWithoutAHost(t *testing.T) {
	app := openApp(t, limits.Limits{})
	s := New(app, nil)
	s.dial = func(config.Config) (*imapclient.Client, error) {
		t.Fatal("dialled without a configured host")
		return nil, nil
	}
	if res, sum := s.scan(config.Config{}); res != (Result{}) || sum.stopped != "no mailbox configured" {
		t.Fatalf("scan = %+v, %+v", res, sum)
	}
}

func TestAddressDefaultsThePortBySecurity(t *testing.T) {
	for _, tc := range []struct{ host, security, want string }{
		{"mail.test", config.IMAPSecurityTLS, "mail.test:993"},
		{"mail.test", config.IMAPSecuritySTARTTLS, "mail.test:143"},
		{"mail.test:1143", config.IMAPSecuritySTARTTLS, "mail.test:1143"},
	} {
		if got := address(config.Config{IMAPHost: tc.host, IMAPSecurity: tc.security}); got != tc.want {
			t.Errorf("address(%q, %s) = %q, want %q", tc.host, tc.security, got, tc.want)
		}
	}
}

func adminID(t *testing.T, app core.App) string {
	t.Helper()
	admin, err := app.FindFirstRecordByFilter("users", "is_app_admin = true")
	if err != nil {
		t.Fatal(err)
	}
	return admin.Id
}

func uidValidity(t *testing.T, m *mailbox) uint32 {
	t.Helper()
	status, err := m.user.Status("INBOX", &imap.StatusOptions{UIDValidity: true})
	if err != nil {
		t.Fatal(err)
	}
	return status.UIDValidity
}

func TestScanSkipsMailReceivedBeforeTheSincePoint(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliverAt(t, "INBOX", time.Now().Add(-48*time.Hour), base64Part("old.txt", "already in the mailbox"))
	m.deliverAt(t, "INBOX", time.Now().Add(-30*time.Minute), base64Part("same-day.txt", "before the save"))
	m.deliver(t, "INBOX", base64Part("new.txt", "arrived after the save"))

	c := cfg(config.IMAPDelete)
	c.IMAPSince = time.Now().Add(-10 * time.Minute)
	s := m.scanner(app)
	if res := s.Scan(c); res != (Result{Created: 1}) {
		t.Fatalf("scan = %+v, want only the mail after the since point", res)
	}
	if got := m.count(t, "INBOX"); got != 2 {
		t.Fatalf("INBOX holds %d, want the two older mails untouched", got)
	}
	if res := s.Scan(c); res != (Result{}) {
		t.Fatalf("second scan = %+v", res)
	}
}

func TestScanRangeBackfillsWithoutMovingOrDeleting(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliverAt(t, "INBOX", time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC), base64Part("jan.txt", "january"))
	m.deliverAt(t, "INBOX", time.Date(2026, 2, 28, 23, 0, 0, 0, time.UTC), base64Part("feb.txt", "february"))
	m.deliverAt(t, "INBOX", time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), base64Part("mar.txt", "march"))

	c := cfg(config.IMAPDelete)
	c.IMAPSince = time.Now()
	s := m.scanner(app)
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	res, err := s.scanRange(c, from, to)
	if err != nil || res != (Result{Created: 2}) {
		t.Fatalf("range scan = %+v, %v; want January and February", res, err)
	}
	if got := m.count(t, "INBOX"); got != 3 {
		t.Fatalf("INBOX holds %d; a backfill must not delete", got)
	}

	// The Management route starts it in the background and polls.
	if err := s.StartBackfill(c, from, to); err != nil {
		t.Fatal(err)
	}
	if err := s.StartBackfill(c, from, to); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start while running = %v, want ErrBusy", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.BackfillStatus().Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got := s.BackfillStatus()
	if got.Running || got.Error != "" || got.Result != (Result{Skipped: 2}) || got.From != "2026-01-01" || got.To != "2026-02-28" {
		t.Fatalf("backfill status = %+v, want both skipped as duplicates over Jan 1 - Feb 28", got)
	}
	if err := s.StartBackfill(config.Config{}, from, to); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("backfill without a mailbox = %v", err)
	}
}

// Without UIDPLUS the only expunge there is purges every \\Deleted message in
// the folder, including ones a mail client flagged and has not expunged yet.
func TestNoUIDPlusFlagsInsteadOfExpungingTheFolder(t *testing.T) {
	for _, after := range []string{config.IMAPDelete, config.IMAPMove} {
		t.Run(after, func(t *testing.T) {
			app := openApp(t, limits.Limits{})
			m := startServer(t, imap.CapIMAP4rev1)
			m.append(t, "INBOX", &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDeleted}}, base64Part("trash.txt", "deleted in a mail client"))
			m.deliver(t, "INBOX", base64Part("a.txt", "consumed"))

			s := m.scanner(app)
			if res := s.Scan(cfg(after)); res != (Result{Created: 1}) {
				t.Fatalf("scan = %+v, want only the live message", res)
			}
			if got := m.count(t, "INBOX"); got != 2 {
				t.Fatalf("INBOX holds %d; nothing may be expunged without UIDPLUS", got)
			}
			if after == config.IMAPMove && m.count(t, "Done") != 1 {
				t.Fatal("the consumed message was not copied to the target")
			}
			if res := m.scanner(app).Scan(cfg(after)); res != (Result{}) {
				t.Fatalf("scan after a restart = %+v; a flagged message must not come back", res)
			}
		})
	}
}

func TestForwardedMessageAttachmentsAreImported(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	forwarded := "Content-Type: message/rfc822\n\n" +
		"From: a@example.com\nSubject: invoice\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=\"inner\"\n\n" +
		"--inner\nContent-Type: text/plain\n\nhere it is\n" +
		"--inner\n" + base64Part("invoice.txt", "inside a forward") +
		"--inner--\n"
	m.deliver(t, "INBOX", forwarded)

	if res := m.scanner(app).Scan(cfg(config.IMAPKeep)); res != (Result{Created: 1}) {
		t.Fatalf("scan = %+v, want the attachment inside the forward", res)
	}
}

// One message that keeps failing is retried, then given up on, so the mail
// behind it is not held back for good.
func TestAFailingMessageIsGivenUpOnAfterMaxAttempts(t *testing.T) {
	app := openApp(t, limits.Limits{})
	app.OnRecordCreate("documents").BindFunc(func(e *core.RecordEvent) error {
		if f, ok := e.Record.Get("file").(*filesystem.File); ok && strings.HasPrefix(f.OriginalName, "stuck") {
			return errors.New("disk full")
		}
		return e.Next()
	})
	m := startServer(t)
	m.deliver(t, "INBOX", base64Part("stuck.txt", "never stored"))
	m.deliver(t, "INBOX", base64Part("behind.txt", "waits its turn"))

	s := m.scanner(app)
	for attempt := 1; attempt < maxAttempts; attempt++ {
		if res := s.Scan(cfg(config.IMAPKeep)); res != (Result{Failed: 1}) {
			t.Fatalf("scan %d = %+v, want the failure retried before anything behind it", attempt, res)
		}
	}
	if res := s.Scan(cfg(config.IMAPKeep)); res != (Result{Created: 1, Failed: 1}) {
		t.Fatalf("scan %d = %+v, want the stuck message given up on and the next imported", maxAttempts, res)
	}
}

func TestSinceToleratesAServerClockBehind(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliverAt(t, "INBOX", time.Now().Add(-2*time.Minute), base64Part("new.txt", "stamped by a slow clock"))

	c := cfg(config.IMAPKeep)
	c.IMAPSince = time.Now()
	if res := m.scanner(app).Scan(c); res != (Result{Created: 1}) {
		t.Fatalf("scan = %+v, want mail within the clock tolerance imported", res)
	}
}

func TestFetchesInBatches(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	for i := range fetchBatch + 3 {
		m.deliver(t, "INBOX", base64Part(fmt.Sprintf("doc%d.txt", i), fmt.Sprintf("document %d", i)))
	}
	if res := m.scanner(app).Scan(cfg(config.IMAPKeep)); res.Created != fetchBatch+3 {
		t.Fatalf("scan = %+v, want every message across batches", res)
	}
}

const (
	onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	twoPixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADklEQVR4nGP4zwAE/xkACP8B/0AhLC4AAAAASUVORK5CYII="
)

func imagePart(disposition, name, b64 string) string {
	return fmt.Sprintf("Content-Type: image/png; name=%q\nContent-ID: <%s>\nContent-Disposition: %s; filename=%q\nContent-Transfer-Encoding: base64\n\n%s\n",
		name, name, disposition, name, b64)
}

func htmlWithLogo() string {
	return "Content-Type: multipart/related; boundary=\"rel\"\n\n" +
		"--rel\nContent-Type: text/html\n\n<img src=\"cid:logo.png\">\n" +
		"--rel\n" + imagePart("inline", "logo.png", base64.StdEncoding.EncodeToString([]byte("not fetched"))) +
		"--rel--\n"
}

func TestInlineImagesAreNeverImported(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliver(t, "INBOX", htmlWithLogo(), base64Part("a.txt", "the document"), imagePart("attachment", "scan.png", onePixelPNG))
	m.deliver(t, "INBOX", htmlWithLogo())
	// Outlook's shape: the logo sits under an alternative nested in the related.
	m.deliver(t, "INBOX", "Content-Type: multipart/related; boundary=\"rel\"\n\n"+
		"--rel\nContent-Type: multipart/alternative; boundary=\"alt\"\n\n"+
		"--alt\nContent-Type: text/plain\n\nhello\n"+
		"--alt\n"+imagePart("inline", "icon.png", base64.StdEncoding.EncodeToString([]byte("not fetched")))+
		"--alt--\n--rel--\n")

	if res := m.scanner(app).Scan(cfg(config.IMAPDelete)); res != (Result{Created: 2}) {
		t.Fatalf("scan = %+v, want the text and the attached image, not the logos", res)
	}
	if got := m.count(t, "INBOX"); got != 2 {
		t.Fatalf("INBOX holds %d, want the two mails with nothing but a logo", got)
	}
}

func TestARelatedImageMarkedAsAttachmentIsImported(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliver(t, "INBOX", "Content-Type: multipart/related; boundary=\"rel\"\n\n"+
		"--rel\nContent-Type: text/html\n\n<p>photo</p>\n"+
		"--rel\n"+imagePart("attachment", "photo.png", twoPixelPNG)+
		"--rel--\n")

	if res := m.scanner(app).Scan(cfg(config.IMAPKeep)); res != (Result{Created: 1}) {
		t.Fatalf("scan = %+v, want the explicitly attached image", res)
	}
}

// A type left unchecked holds the message like a refusal, so delete cannot
// take an attachment the user never imported; an inline logo does not.
func TestSkippedFileTypesKeepTheMessage(t *testing.T) {
	app := openApp(t, limits.Limits{})
	m := startServer(t)
	m.deliver(t, "INBOX", base64Part("a.txt", "kept beside a photo"), imagePart("attachment", "scan.png", onePixelPNG))
	m.deliver(t, "INBOX", htmlWithLogo(), base64Part("b.txt", "beside a logo only"))
	m.deliver(t, "INBOX", imagePart("attachment", "only.png", twoPixelPNG))

	c := cfg(config.IMAPDelete)
	c.IMAPSkipTypes = []string{"image"}
	if res := m.scanner(app).Scan(c); res != (Result{Created: 2}) {
		t.Fatalf("scan = %+v, want both texts and no image", res)
	}
	if got := m.count(t, "INBOX"); got != 2 {
		t.Fatalf("INBOX holds %d, want the two messages with a skipped image", got)
	}
}

func TestEveryFileTypeExtensionIsStorable(t *testing.T) {
	for name, exts := range config.IMAPFileTypes {
		for _, ext := range exts {
			if !zipimport.Storable(ext) {
				t.Errorf("%s (%s) is not storable", ext, name)
			}
		}
	}
}
