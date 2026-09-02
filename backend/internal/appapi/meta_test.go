package appapi

import (
	"testing"

	"github.com/pocketbase/pocketbase"
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
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
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
	if err := first.ResetBootstrapState(); err != nil {
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
	if err := first.ResetBootstrapState(); err != nil {
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
