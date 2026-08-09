package e2e

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"paperless-go/backend/internal/appapi"
	"paperless-go/backend/internal/appwire"
	"paperless-go/backend/internal/config"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"

	_ "paperless-go/backend/migrations"
)

const (
	UserEmail     = "e2e@paperless.local"
	UserPassword  = "e2epassword123"
	SuperEmail    = "admin@paperless.local"
	SuperPassword = "adminpassword123"
)

// Harness is a live PocketBase app with mocked OCR/AI providers.
type Harness struct {
	BaseURL   string
	DataDir   string
	PublicDir string
	App       *pocketbase.PocketBase
	HTTP      *http.Client
	Mocks     *mockServers

	UserID      string
	SuperID     string
	AdminUserID string

	cancelServe func()
}

var (
	sharedMu      sync.Mutex
	sharedHarness *Harness
)

// Options configures Start.
type Options struct {
	// PublicDir is the SPA static directory. Empty uses a temp empty dir.
	PublicDir string
	// HTTPAddr forces a listen address (e.g. 127.0.0.1:8090). Empty uses an ephemeral port.
	HTTPAddr string
	// SkipAuthSeed skips creating the default e2e user and superuser.
	SkipAuthSeed bool
	// EmptyAPIKeys leaves OCR/AI API keys unset (for setup wizard tests).
	EmptyAPIKeys bool
}

// Start boots a temporary PocketBase instance with mocks and seeded users.
// Prefer StartShared from tests; use Start for the Playwright e2e server process.
func Start(opts Options) (*Harness, error) {
	mocks := startMockServers()

	dataDir, err := os.MkdirTemp("", "paperless-e2e-*")
	if err != nil {
		mocks.Close()
		return nil, err
	}

	publicDir := opts.PublicDir
	if publicDir == "" {
		publicDir = filepath.Join(dataDir, "public")
		if err := os.MkdirAll(publicDir, 0o755); err != nil {
			mocks.Close()
			_ = os.RemoveAll(dataDir)
			return nil, err
		}
		_ = os.WriteFile(filepath.Join(publicDir, "index.html"), []byte("<!doctype html><title>e2e</title>"), 0o644)
	}

	// Seed env before Runtime/bootstrap so app_settings and clients point at mocks.
	_ = os.Setenv("OCR_PROVIDER", "mistral")
	if opts.EmptyAPIKeys {
		_ = os.Unsetenv("MISTRAL_API_KEY")
		_ = os.Unsetenv("OPENAI_API_KEY")
		_ = os.Unsetenv("GOOGLE_VISION_API_KEY")
	} else {
		_ = os.Setenv("MISTRAL_API_KEY", "e2e-mistral-key")
		_ = os.Setenv("OPENAI_API_KEY", "e2e-openai-key")
		_ = os.Unsetenv("GOOGLE_VISION_API_KEY")
	}
	_ = os.Setenv("MISTRAL_API_BASE_URL", mocks.OCR.URL+"/v1")
	_ = os.Setenv("MISTRAL_OCR_MODEL", "mistral-ocr-latest")
	_ = os.Setenv("OPENAI_BASE_URL", mocks.OpenAI.URL+"/v1")
	_ = os.Setenv("OPENAI_MODEL", "e2e-mock")
	_ = os.Setenv("OPENAI_CHAT_MODEL", "e2e-mock")
	_ = os.Setenv("OPENAI_SEARCH_MODEL", "e2e-mock")
	_ = os.Setenv("OPENAI_TIMEOUT_SEC", "30")
	_ = os.Setenv("OCR_TIMEOUT_SEC", "30")
	_ = os.Setenv("WORKER_TIMEOUT_SEC", "120")
	_ = os.Setenv("WORKER_MAX_RETRIES", "0")
	_ = os.Setenv("WORKER_CRON_EXPR", "0 0 1 1 *") // effectively never during short tests
	_ = os.Setenv("DEEP_SEARCH_LANGUAGES", "en")
	_ = os.Unsetenv("VITE_DEV_USER_EMAIL")
	_ = os.Unsetenv("VITE_DEV_USER_PASSWORD")

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  dataDir,
		HideStartBanner: true,
		DefaultDev:      false,
	})

	rt := config.NewRuntime()
	appwire.Register(app, rt, publicDir, true)

	listenAddr := opts.HTTPAddr
	var listener net.Listener
	if listenAddr == "" {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			mocks.Close()
			_ = os.RemoveAll(dataDir)
			return nil, err
		}
		listenAddr = listener.Addr().String()
	}

	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: -10000,
		Func: func(e *core.ServeEvent) error {
			e.InstallerFunc = nil
			if listener != nil {
				e.Listener = listener
				e.Server.Addr = listenAddr
			}
			return e.Next()
		},
	})

	if err := app.Bootstrap(); err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		mocks.Close()
		_ = os.RemoveAll(dataDir)
		return nil, fmt.Errorf("bootstrap: %w", err)
	}

	settings := app.Settings()
	settings.Meta.AppName = "Paperless Go"
	if err := app.Save(settings); err != nil {
		_ = app.ResetBootstrapState()
		if listener != nil {
			_ = listener.Close()
		}
		mocks.Close()
		_ = os.RemoveAll(dataDir)
		return nil, fmt.Errorf("save app settings: %w", err)
	}

	// EmptyAPIKeys only affects first-boot seed; restore mock keys so other harnesses
	// in this process (e.g. shared) are not left without env fallbacks.
	if opts.EmptyAPIKeys {
		_ = os.Setenv("MISTRAL_API_KEY", "e2e-mistral-key")
		_ = os.Setenv("OPENAI_API_KEY", "e2e-openai-key")
	}

	var userID, superID, adminUserID string
	if !opts.SkipAuthSeed {
		var err error
		userID, err = createAuthRecord(app, "users", UserEmail, UserPassword)
		if err != nil {
			_ = app.ResetBootstrapState()
			if listener != nil {
				_ = listener.Close()
			}
			mocks.Close()
			_ = os.RemoveAll(dataDir)
			return nil, fmt.Errorf("create user: %w", err)
		}
		superID, err = createAuthRecord(app, core.CollectionNameSuperusers, SuperEmail, SuperPassword)
		if err != nil {
			_ = app.ResetBootstrapState()
			if listener != nil {
				_ = listener.Close()
			}
			mocks.Close()
			_ = os.RemoveAll(dataDir)
			return nil, fmt.Errorf("create superuser: %w", err)
		}
		adminRec, err := appapi.UpsertPairedUser(app, SuperEmail, SuperPassword)
		if err != nil {
			_ = app.ResetBootstrapState()
			if listener != nil {
				_ = listener.Close()
			}
			mocks.Close()
			_ = os.RemoveAll(dataDir)
			return nil, fmt.Errorf("create paired admin user: %w", err)
		}
		adminUserID = adminRec.Id
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- apis.Serve(app, apis.ServeConfig{
			HttpAddr:        listenAddr,
			ShowStartBanner: false,
		})
	}()

	baseURL := "http://" + listenAddr
	client := &http.Client{Timeout: 30 * time.Second}
	if err := waitReady(client, baseURL, 15*time.Second); err != nil {
		_ = app.OnTerminate().Trigger(&core.TerminateEvent{App: app}, func(e *core.TerminateEvent) error {
			return e.Next()
		})
		select {
		case <-serveErr:
		case <-time.After(2 * time.Second):
		}
		mocks.Close()
		_ = os.RemoveAll(dataDir)
		return nil, fmt.Errorf("server not ready: %w", err)
	}

	h := &Harness{
		BaseURL:     baseURL,
		DataDir:     dataDir,
		PublicDir:   publicDir,
		App:         app,
		HTTP:        client,
		Mocks:       mocks,
		UserID:      userID,
		SuperID:     superID,
		AdminUserID: adminUserID,
		cancelServe: func() {
			_ = app.OnTerminate().Trigger(&core.TerminateEvent{App: app}, func(e *core.TerminateEvent) error {
				return e.Next()
			})
			select {
			case <-serveErr:
			case <-time.After(3 * time.Second):
			}
		},
	}
	return h, nil
}

// Close shuts down the server and removes the temp data directory.
func (h *Harness) Close() {
	if h == nil {
		return
	}
	if h.cancelServe != nil {
		h.cancelServe()
	}
	if h.Mocks != nil {
		h.Mocks.Close()
	}
	if h.DataDir != "" {
		_ = os.RemoveAll(h.DataDir)
	}
}

// StartShared returns a package-level harness for Go e2e tests (TestMain).
func StartShared(t testing.TB) *Harness {
	t.Helper()
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedHarness == nil {
		t.Fatal("shared harness not started; TestMain missing?")
	}
	return sharedHarness
}

func initShared() error {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedHarness != nil {
		return nil
	}
	h, err := Start(Options{})
	if err != nil {
		return err
	}
	sharedHarness = h
	return nil
}

func closeShared() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedHarness != nil {
		sharedHarness.Close()
		sharedHarness = nil
	}
}

func createAuthRecord(app core.App, collectionName, email, password string) (string, error) {
	collection, err := app.FindCollectionByNameOrId(collectionName)
	if err != nil {
		return "", err
	}
	record := core.NewRecord(collection)
	record.SetEmail(email)
	record.SetPassword(password)
	record.SetVerified(true)
	if err := app.Save(record); err != nil {
		return "", err
	}
	return record.Id, nil
}

func waitReady(client *http.Client, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/api/health", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode > 0 {
				return nil
			}
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout waiting for %s", baseURL)
	}
	return lastErr
}
