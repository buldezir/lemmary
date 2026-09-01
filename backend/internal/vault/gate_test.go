package vault

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newGateHarness(t *testing.T) (*Vault, chan GateResult, http.Handler) {
	t.Helper()
	h := newHarness(t)
	done := make(chan GateResult, 1)
	return h.v, done, h.v.gateHandler(done)
}

// A locked instance must not look like an empty one. Answering API calls with an
// ordinary empty 200 would have a client render an archive with no documents in
// it and, worse, could let a sync client conclude everything was deleted.
func TestGateLockedAPIReturns423(t *testing.T) {
	_, _, handler := newGateHarness(t)

	for _, path := range []string{
		"/api/collections/documents/records",
		"/api/app/documents/export",
		"/api/files/documents/abc/def.pdf",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusLocked {
			t.Errorf("%s returned %d, want 423", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"locked":true`) {
			t.Errorf("%s body does not say it is locked: %s", path, rec.Body.String())
		}
	}
}

// The container healthcheck must treat locked as healthy: an instance waiting
// for its first sign-in is working as designed, and reporting unhealthy would
// make Docker restart-loop it forever, so nobody could ever unlock it.
func TestGateHealthIsOKWhileLocked(t *testing.T) {
	_, _, handler := newGateHarness(t)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health returned %d while locked, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"locked":true`) {
		t.Fatalf("health does not report the locked state: %s", rec.Body.String())
	}
}

func TestGateUnlockRejectsWrongCredential(t *testing.T) {
	v, done, handler := newGateHarness(t)

	// The harness vault is already initialised with this password.
	body := `{"password":"definitely-not-the-password"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password returned %d, want 401", rec.Code)
	}
	// The response must not distinguish a wrong password from a tampered
	// keyring, and must not name a wrap or leak key material.
	lower := strings.ToLower(rec.Body.String())
	for _, leak := range []string{"wrap", "argon", "keyring", v.kr.MKFP} {
		if leak != "" && strings.Contains(lower, strings.ToLower(leak)) {
			t.Fatalf("the failure response leaks %q: %s", leak, rec.Body.String())
		}
	}
	select {
	case <-done:
		t.Fatal("a failed unlock signalled success")
	default:
	}
}

func TestGateUnlockAcceptsTheRightCredential(t *testing.T) {
	_, done, handler := newGateHarness(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader(`{"password":"test-password"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unlock returned %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case <-done:
	default:
		t.Fatal("a successful unlock did not signal the gate")
	}
}

func TestGateUnlockRejectsNonPost(t *testing.T) {
	_, _, handler := newGateHarness(t)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unlock", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /unlock returned %d, want 405", rec.Code)
	}
}

// The unlock page is served before any template data could be attacker
// controlled, but pin that: the only value it interpolates is a bool.
func TestGateUnlockPageRendersWithoutUserInput(t *testing.T) {
	_, _, handler := newGateHarness(t)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / returned %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<form id=\"f\">") {
		t.Fatalf("the unlock form is missing: %s", body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	// This vault is initialised, so the field asks for a password that already
	// exists and must offer to autofill it.
	if !strings.Contains(body, `autocomplete="current-password"`) {
		t.Fatalf("the unlock form does not offer to autofill: %s", body)
	}
}

// The same field on a fresh vault is asking the visitor to *choose* the
// password that will encrypt everything. current-password there makes a
// password manager offer some unrelated saved credential instead of generating
// a strong one, which is the opposite of what this one moment needs -- and it
// is the only moment, because the password cannot be changed afterwards
// without the archive.
func TestGateSetupPageAsksForAGeneratedPassword(t *testing.T) {
	root := t.TempDir()
	v, err := New(Options{
		Dir: filepath.Join(root, "vault"), WorkDir: filepath.Join(root, "work"),
		Enabled: true, AllowDiskWorkDir: true,
		Log: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.releaseLock()
	if v.Initialized() {
		t.Fatal("a fresh directory reports initialised; this test is not exercising the setup branch")
	}

	rec := httptest.NewRecorder()
	v.gateHandler(make(chan GateResult, 1)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "Set up encryption") {
		t.Fatalf("not the setup page: %s", body)
	}
	if !strings.Contains(body, `autocomplete="new-password"`) {
		t.Fatalf("the setup form does not ask for a generated password: %s", body)
	}
}

// An uninitialised vault mints a recovery code on first unlock, and it must be
// returned exactly once so an operator can write it down.
func TestGateInitialisationReturnsARecoveryCodeOnce(t *testing.T) {
	root := t.TempDir()
	v, err := New(Options{
		Dir: root + "/vault", WorkDir: root + "/work",
		Enabled: true, AllowDiskWorkDir: true,
		Log: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.releaseLock()

	done := make(chan GateResult, 1)
	handler := v.gateHandler(done)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader(`{"password":"first-password"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("initialisation returned %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Initialized  bool   `json:"initialized"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Initialized || out.RecoveryCode == "" {
		t.Fatalf("no recovery code on initialisation: %s", rec.Body.String())
	}

	// That code must genuinely open the vault.
	if _, _, err := v.Keyring().Unlock(Credential{RecoveryCode: out.RecoveryCode}); err != nil {
		t.Fatalf("the issued recovery code does not unlock: %v", err)
	}
}

// An empty password must not initialise a vault with an empty credential.
func TestGateRefusesEmptyInitialisationPassword(t *testing.T) {
	root := t.TempDir()
	v, err := New(Options{
		Dir: root + "/vault", WorkDir: root + "/work",
		Enabled: true, AllowDiskWorkDir: true,
		Log: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.releaseLock()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader(`{"password":""}`))
	req.Header.Set("Content-Type", "application/json")
	v.gateHandler(make(chan GateResult, 1)).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty password returned %d, want 400", rec.Code)
	}
	if v.Initialized() {
		t.Fatal("an empty password initialised the vault")
	}
}

// Initialising an empty vault mints a master key under a caller-chosen
// password. Nothing authenticates that request, so it must at least be
// unreachable from a hostile page in the operator's browser: a form POST is a
// CORS simple request and would otherwise let any site seize the key of a fresh
// instance on localhost or the LAN.
func TestGateUnlockRejectsCrossOriginAndFormPosts(t *testing.T) {
	root := t.TempDir()
	v, err := New(Options{
		Dir: root + "/vault", WorkDir: root + "/work",
		Enabled: true, AllowDiskWorkDir: true, Log: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.releaseLock()
	handler := v.gateHandler(make(chan GateResult, 1))

	// The drive-by: a hidden form auto-submitted by an unrelated page.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader("password=attacker-chosen"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a form-encoded POST returned %d, want 403", rec.Code)
	}
	if v.Initialized() {
		t.Fatal("a cross-site form POST initialised the vault")
	}

	// Explicitly cross-site, even as JSON.
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Origin": "https://evil.example"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader(`{"password":"attacker-chosen"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Host = "archive.example"
		for k, val := range h {
			req.Header.Set(k, val)
		}
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%v returned %d, want 403", h, rec.Code)
		}
		if v.Initialized() {
			t.Fatalf("%v initialised the vault", h)
		}
	}

	// A same-origin JSON request from the served page still works.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/unlock", strings.NewReader(`{"password":"legitimate"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("a same-origin unlock returned %d: %s", rec.Code, rec.Body.String())
	}
}

// Switching encryption on for a directory that already holds an unencrypted
// install would start with an empty archive and strand the real data beside it
// in the clear.
func TestInitRefusesAnExistingPlaintextInstall(t *testing.T) {
	for name, seed := range map[string]func(dir string) error{
		"a database": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "data.db"), []byte("SQLite format 3\x00"), 0o600)
		},
		"stored files": func(dir string) error {
			if err := os.MkdirAll(filepath.Join(dir, "storage", "pbc_1"), 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "storage", "pbc_1", "doc.pdf"), []byte("%PDF"), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "pb_data")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := seed(dir); err != nil {
				t.Fatalf("seed: %v", err)
			}

			v, err := New(Options{
				Dir: dir, WorkDir: filepath.Join(root, "work"),
				Enabled: true, AllowDiskWorkDir: true, Log: func(string, ...any) {},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer v.releaseLock()

			_, err = v.Init("u1", "password-for-this-test")
			if err == nil {
				t.Fatal("initialising over an unencrypted install was allowed")
			}
			if !strings.Contains(err.Error(), "unencrypted install") {
				t.Fatalf("the error must name the problem: %v", err)
			}
			if v.Initialized() {
				t.Fatal("the refused init still marked the vault initialised")
			}
		})
	}
}

// A passphrase that no longer opens anything must fall through to the unlock
// form, not fail startup.
//
// The way to reach this is not a typo. Provisioning with VAULT_PASSPHRASE set is
// documented, and the first account save deliberately revokes the bootstrap wrap
// that passphrase created. Leave the variable in the compose file — the natural
// thing to do — and a hard failure here exits 1 on every restart, crash-looping
// the container under any restart policy, with the form that would have accepted
// an ordinary account password never served.
func TestGateFallsBackToTheFormWhenTheEnvPassphraseIsStale(t *testing.T) {
	h := newHarness(t)
	t.Setenv(EnvPassphrase, "the-revoked-bootstrap-passphrase")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// The gate must reach the point of serving and then wait, so it is the
	// context deadline that ends this, not an unlock error.
	_, err := h.v.Gate(ctx, "127.0.0.1:0", false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Gate returned %v, want it to have served the form and waited", err)
	}

	var served bool
	for _, line := range h.logs {
		if strings.Contains(line, "waiting for a sign-in") {
			served = true
		}
	}
	if !served {
		t.Fatalf("the gate never served the unlock form; logs: %v", h.logs)
	}
}

// A passphrase that does work must still unlock without serving anything, which
// is what CLI subcommands and the test suite rely on.
func TestGateUnlocksFromTheEnvironmentWhenThePassphraseIsCurrent(t *testing.T) {
	root := t.TempDir()
	dir, workDir := filepath.Join(root, "vault"), filepath.Join(root, "work")
	opts := Options{Dir: dir, WorkDir: workDir, Enabled: true, AllowDiskWorkDir: true}

	v, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := v.Init("", "provisioning-passphrase"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	v.releaseLock()

	v, err = New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer v.releaseLock()

	t.Setenv(EnvPassphrase, "provisioning-passphrase")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := v.Gate(ctx, "127.0.0.1:0", false); err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !v.Loaded() {
		t.Fatal("the gate returned without unlocking")
	}
}

// The refusal to serve the unlock form in the clear has to fire for the address
// the stock container actually uses. Its entrypoint passes
// --http=0.0.0.0:${PORT}, and an explicit address used to be exempt — so the
// check never fired on the one configuration most people run.
func TestGateRefusesAnExposedCleartextAddress(t *testing.T) {
	h := newHarness(t)
	t.Setenv(EnvPassphrase, "")

	_, err := h.v.Gate(context.Background(), "0.0.0.0:80", true)
	if err == nil {
		t.Fatal("the gate served the unlock form on an exposed cleartext address")
	}
	if !strings.Contains(err.Error(), EnvAllowInsecureGate) {
		t.Fatalf("the refusal does not name the escape hatch: %v", err)
	}

	// And the escape hatch has to work, or an operator who accepts the risk
	// cannot start at all.
	h.v.opts.AllowInsecureGate = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ctxErr(h.v.Gate(ctx, "127.0.0.1:0", true)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("with the escape hatch set the gate returned %v, want it to have served", err)
	}
}

func ctxErr(_ GateResult, err error) error { return err }
