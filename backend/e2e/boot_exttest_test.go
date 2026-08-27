//go:build lemmary_exttest

package e2e

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"lemmary/backend/internal/boot"
)

// These tests run only under the lemmary_exttest tag, against the throwaway
// pre-boot step in internal/boot/boot_exttest.go. They exist so a change that
// stops honouring one of boot.Result's fields fails here rather than in a
// private fork's CI weeks later.
//
//	go test -tags lemmary_exttest ./e2e/ -run Boot
//
// internal/boot's own tests assert what Prepare returns. These assert that a
// live app actually runs on it: the data directory really moved, and the
// registration really bound behind the core routes.

// extTestBoot drives the seam the way main.go does — argv in, Result out.
func extTestBoot(dataDir string) (boot.Result, error) {
	return boot.Prepare([]string{"serve", "--dir", dataDir})
}

func TestBootRelocatesTheLiveDataDir(t *testing.T) {
	h, err := Start(Options{Boot: extTestBoot})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Close()

	want := filepath.Join(h.DataDir, boot.ExtTestDataDirName)
	if got := h.App.DataDir(); got != want {
		t.Fatalf("app data dir = %q, want %q", got, want)
	}

	// The relocation is only real if the database went with it. Reading a
	// seeded record proves the app bootstrapped against the moved directory
	// rather than reporting one path and using another.
	if _, err := h.App.FindAuthRecordByEmail("users", UserEmail); err != nil {
		t.Fatalf("seeded user not found in the relocated data dir: %v", err)
	}
	if _, err := h.App.FindCollectionByNameOrId("documents"); err != nil {
		t.Fatalf("migrations did not run in the relocated data dir: %v", err)
	}
}

func TestBootRegisterBindsAgainstTheLiveApp(t *testing.T) {
	h, err := Start(Options{Boot: extTestBoot})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Close()

	status, raw := h.doJSON(t, http.MethodGet, "/api/exttest/boot", h.userToken(t), nil)
	requireStatus(t, status, http.StatusOK, raw)

	var body struct {
		Boot    string `json:"boot"`
		DataDir string `json:"data_dir"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if body.Boot != "exttest" {
		t.Fatalf("boot route answered for %q", body.Boot)
	}
	// The route was bound by Result.Register and can see the app it was handed,
	// which is the difference between being registered and merely existing.
	if !strings.HasSuffix(body.DataDir, boot.ExtTestDataDirName) {
		t.Fatalf("registration saw data dir %q, want one ending in %q", body.DataDir, boot.ExtTestDataDirName)
	}
}

// A harness without the option must run the untouched path, because that is
// what every other test in this package is asserting against.
func TestWithoutBootTheDefaultPathIsUnchanged(t *testing.T) {
	h := StartShared(t)

	if got := h.App.DataDir(); got != h.DataDir {
		t.Fatalf("app data dir = %q, want the harness dir %q", got, h.DataDir)
	}
	// Asserted on the payload, not the status: an unrouted /api path falls
	// through to the OpenAPI document rather than 404, so a status check here
	// would pass whether or not the route was bound.
	_, raw := h.doJSON(t, http.MethodGet, "/api/exttest/boot", h.userToken(t), nil)
	var body struct {
		Boot string `json:"boot"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err == nil && body.Boot != "" {
		t.Fatalf("boot route was bound on a harness with no pre-boot step: %s", raw)
	}
}

func TestBootCloseRunsWhenTheHarnessCloses(t *testing.T) {
	before := boot.ExtTestCloseCount()

	h, err := Start(Options{Boot: extTestBoot})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	h.Close()

	if got := boot.ExtTestCloseCount() - before; got != 1 {
		t.Fatalf("Close ran %d times, want 1", got)
	}
}
