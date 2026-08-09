package appapi

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	pbcmd "github.com/pocketbase/pocketbase/cmd"
)

func TestRegisterSystemCommandsWrapsSuperuserHooks(t *testing.T) {
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})

	RegisterSystemCommands(app, false)

	for _, name := range []string{"upsert", "create", "update", "delete"} {
		c, _, err := app.RootCmd.Find([]string{"superuser", name})
		if err != nil {
			t.Fatalf("find superuser %s: %v", name, err)
		}
		if c == nil || c.RunE == nil {
			t.Fatalf("superuser %s missing RunE after RegisterSystemCommands", name)
		}
	}

	serve, _, err := app.RootCmd.Find([]string{"serve"})
	if err != nil || serve == nil {
		t.Fatalf("serve command missing: %v", err)
	}
}

func TestWrapBeforeStartIsNoOp(t *testing.T) {
	// Documents the old bug: wrapping before Start finds nothing because
	// PocketBase registers superuser commands inside Start.
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})

	c, _, err := app.RootCmd.Find([]string{"superuser", "upsert"})
	if err == nil && c != nil && c.RunE != nil {
		t.Fatal("expected superuser upsert to be absent before Start/RegisterSystemCommands")
	}

	// Contrast: after explicit registration the command exists.
	app.RootCmd.AddCommand(pbcmd.NewSuperuserCommand(app))
	c, _, err = app.RootCmd.Find([]string{"superuser", "upsert"})
	if err != nil || c == nil || c.RunE == nil {
		t.Fatalf("expected upsert after AddCommand: err=%v cmd=%v", err, c)
	}
}
