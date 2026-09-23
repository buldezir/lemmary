package imapimport

import (
	"bytes"
	"encoding/base64"
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

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/inflight"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/testpb"
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

func startServer(t *testing.T) *mailbox {
	t.Helper()
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
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}},
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
	s := newScanner(app, nil)
	s.dial = func(config.Config) (*imapclient.Client, error) { return imapclient.DialInsecure(m.addr, nil) }
	return s
}

func (m *mailbox) deliver(t *testing.T, folder string, parts ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("From: sender@example.com\nTo: docs@example.com\nSubject: scan\nMIME-Version: 1.0\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"b\"\n\n--b\nContent-Type: text/plain\n\nsee attached\n")
	for _, p := range parts {
		b.WriteString("--b\n" + p)
	}
	b.WriteString("--b--\n")
	raw := []byte(strings.ReplaceAll(b.String(), "\n", "\r\n"))
	if _, err := m.user.Append(folder, bytes.NewReader(raw), &imap.AppendOptions{}); err != nil {
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
	s := newScanner(app, nil)
	s.dial = func(config.Config) (*imapclient.Client, error) {
		t.Fatal("dialled without a configured host")
		return nil, nil
	}
	if res := s.Scan(config.Config{}); res != (Result{}) {
		t.Fatalf("scan = %+v", res)
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
