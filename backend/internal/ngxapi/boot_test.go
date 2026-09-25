package ngxapi

import (
	"testing"

	"github.com/pocketbase/pocketbase"

	"lemmary/backend/internal/ngxid"
	"lemmary/backend/internal/testpb"

	_ "lemmary/backend/migrations"
)

func bootTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ClearBootstrap() })
	return app
}

func bootSchemaTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := testpb.Open(t)
	ngxid.Register(app)
	return app
}
