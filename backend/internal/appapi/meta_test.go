package appapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/updatecheck"
)

func bootNamedApp(t *testing.T, dataDir string, register bool) *pocketbase.PocketBase {
	t.Helper()
	if dataDir == "" {
		dataDir = t.TempDir()
	}
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  dataDir,
		HideStartBanner: true,
	})
	if register {
		RegisterAppName(app)
	}
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ClearBootstrap() })
	return app
}

func TestRegisterAppNamePersistsLemmaryOnFreshInstall(t *testing.T) {
	app := bootNamedApp(t, "", true)
	if got := app.Settings().Meta.AppName; got != defaultAppName {
		t.Fatalf("stored AppName = %q, want %q", got, defaultAppName)
	}
}

func TestRegisterAppNameLeavesACustomNameAlone(t *testing.T) {
	dir := t.TempDir()
	first := bootNamedApp(t, dir, false)
	first.Settings().Meta.AppName = "My Archive"
	if err := first.Save(first.Settings()); err != nil {
		t.Fatal(err)
	}
	if err := first.ClearBootstrap(); err != nil {
		t.Fatal(err)
	}

	second := bootNamedApp(t, dir, true)
	if got := second.Settings().Meta.AppName; got != "My Archive" {
		t.Fatalf("stored AppName = %q, want My Archive", got)
	}
}

func TestRegisterAppNameRewritesStoredAcme(t *testing.T) {
	dir := t.TempDir()
	first := bootNamedApp(t, dir, false)
	if got := first.Settings().Meta.AppName; got != pocketBaseDefaultAppName {
		t.Fatalf("PocketBase default AppName = %q, want %q", got, pocketBaseDefaultAppName)
	}
	if err := first.ClearBootstrap(); err != nil {
		t.Fatal(err)
	}

	second := bootNamedApp(t, dir, true)
	if got := second.Settings().Meta.AppName; got != defaultAppName {
		t.Fatalf("stored AppName = %q, want %q", got, defaultAppName)
	}
}

func TestResolvedAppName(t *testing.T) {
	app := bootNamedApp(t, "", false)

	if got := resolvedAppName(app); got != defaultAppName {
		t.Fatalf("Acme placeholder = %q, want %q", got, defaultAppName)
	}

	app.Settings().Meta.AppName = ""
	if got := resolvedAppName(app); got != defaultAppName {
		t.Fatalf("empty = %q, want %q", got, defaultAppName)
	}

	app.Settings().Meta.AppName = "  My Archive  "
	if got := resolvedAppName(app); got != "My Archive" {
		t.Fatalf("custom = %q, want My Archive", got)
	}

	if got := resolvedAppName(nil); got != defaultAppName {
		t.Fatalf("nil app = %q, want %q", got, defaultAppName)
	}
}

// PocketBase installs its own blue, which would otherwise be what a fresh
// instance shows in its logo mark.
func TestRegisterAppNameSeedsTheAccent(t *testing.T) {
	app := bootNamedApp(t, "", true)
	if got := app.Settings().Meta.AccentColor; got != defaultAccent {
		t.Fatalf("stored AccentColor = %q, want %q", got, defaultAccent)
	}
}

func TestRegisterAppNameLeavesAChosenAccentAlone(t *testing.T) {
	dir := t.TempDir()
	first := bootNamedApp(t, dir, false)
	first.Settings().Meta.AccentColor = "#123456"
	if err := first.Save(first.Settings()); err != nil {
		t.Fatal(err)
	}
	if err := first.ClearBootstrap(); err != nil {
		t.Fatal(err)
	}

	second := bootNamedApp(t, dir, true)
	if got := second.Settings().Meta.AccentColor; got != "#123456" {
		t.Fatalf("stored AccentColor = %q, want #123456", got)
	}
}

// Not parallel: t.Setenv, and NewRuntime writes the process-global managed flag.
func TestMetaCarriesTheImageVersion(t *testing.T) {
	prev := aiprovider.Managed()
	t.Cleanup(func() { aiprovider.SetManaged(prev) })
	t.Setenv(updatecheck.EnvVersion, "v2026.10.13")
	t.Setenv(updatecheck.EnvRevision, "570da0c7abc")

	handler := handleGetMeta(bootNamedApp(t, "", false), config.NewRuntime(config.AIEnv{}), false, false)
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Response = rec
	e.Request = httptest.NewRequest(http.MethodGet, "/api/app/meta", nil)
	if err := handler(e); err != nil {
		t.Fatal(err)
	}

	var meta map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["version"] != "v2026.10.13" || meta["revision"] != "570da0c7abc" {
		t.Fatalf("version = %v, revision = %v", meta["version"], meta["revision"])
	}
}
