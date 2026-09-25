// Package testpb opens a PocketBase app for tests from a copied, already-migrated
// data directory.
//
// Callers that need the Lemmary schema must blank-import lemmary/backend/migrations
// so RunAppMigrations has something to record into the template. This package
// does not import migrations itself: the migrations package's own tests would
// otherwise cycle.
package testpb

import (
	"os"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase"
)

// Open Bootstraps a PocketBase whose data dir is a copy of a once-migrated
// template. Each test still gets its own SQLite files.
func Open(t testing.TB) *pocketbase.PocketBase {
	t.Helper()
	src, err := schemaTemplate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatalf("copy schema template: %v", err)
	}
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  dir,
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ClearBootstrap() })
	return app
}

var (
	schemaOnce sync.Once
	schemaDir  string
	schemaErr  error
)

func schemaTemplate() (string, error) {
	schemaOnce.Do(func() {
		dir, err := os.MkdirTemp("", "testpb-schema-*")
		if err != nil {
			schemaErr = err
			return
		}
		app := pocketbase.NewWithConfig(pocketbase.Config{
			DefaultDataDir:  dir,
			HideStartBanner: true,
		})
		if err := app.Bootstrap(); err != nil {
			schemaErr = err
			return
		}
		if err := app.RunAppMigrations(); err != nil {
			_ = app.ClearBootstrap()
			schemaErr = err
			return
		}
		schemaErr = app.ClearBootstrap()
		schemaDir = dir
	})
	return schemaDir, schemaErr
}
