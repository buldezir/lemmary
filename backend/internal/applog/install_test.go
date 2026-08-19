package applog

import (
	"context"
	"log/slog"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	pblogger "github.com/pocketbase/pocketbase/tools/logger"
)

func bootTestApp(t *testing.T, dev bool) *pocketbase.PocketBase {
	t.Helper()
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
		DefaultDev:      dev,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	return app
}

func TestInstallNoOpWhenLogLevelUnset(t *testing.T) {
	t.Setenv(EnvLogLevel, "")

	app := bootTestApp(t, false)
	before := app.Logger().Handler()
	if _, ok := before.(*pblogger.BatchHandler); !ok {
		t.Fatalf("pre-install handler type %T, want *logger.BatchHandler", before)
	}

	Install(app)

	if app.Logger().Handler() != before {
		t.Fatalf("handler changed when LOG_LEVEL unset: %T", app.Logger().Handler())
	}
}

func TestInstallNoOpWhenDev(t *testing.T) {
	t.Setenv(EnvLogLevel, "debug")

	app := bootTestApp(t, true)
	before := app.Logger().Handler()
	Install(app)
	if app.Logger().Handler() != before {
		t.Fatalf("handler changed in --dev: %T", app.Logger().Handler())
	}
}

func TestInstallWrapsWhenLogLevelSet(t *testing.T) {
	t.Setenv(EnvLogLevel, "debug")

	app := bootTestApp(t, false)
	Install(app)

	h, ok := app.Logger().Handler().(*teeHandler)
	if !ok {
		t.Fatalf("handler type %T, want *teeHandler", app.Logger().Handler())
	}
	if innerBatchHandler(h) == nil {
		t.Fatal("tee inner is not a BatchHandler")
	}
}

func TestInstallDoesNotWrapNonBatchHandler(t *testing.T) {
	t.Setenv(EnvLogLevel, "debug")

	app := bootTestApp(t, false)
	fallback := slog.Default()
	if !setAppLogger(app, fallback) {
		t.Fatal("failed to replace logger with slog.Default")
	}
	before := app.Logger().Handler()

	Install(app)

	if app.Logger().Handler() != before {
		t.Fatalf("wrapped non-BatchHandler: %T", app.Logger().Handler())
	}
}

func TestInstallSettingsReloadUpdatesInnerMinLevel(t *testing.T) {
	t.Setenv(EnvLogLevel, "debug")

	app := bootTestApp(t, false)
	Install(app)

	ctx := context.Background()
	inner := innerBatchHandler(app.Logger().Handler())
	if inner == nil {
		t.Fatal("expected inner BatchHandler")
	}
	if !inner.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("info should be enabled at default DB min level")
	}

	app.Settings().Logs.MinLevel = int(slog.LevelError)
	if err := app.Save(app.Settings()); err != nil {
		t.Fatal(err)
	}

	inner = innerBatchHandler(app.Logger().Handler())
	if inner.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("info should be disabled after DB minLevel=error")
	}
	if !inner.Enabled(ctx, slog.LevelError) {
		t.Fatal("error should stay enabled after DB minLevel=error")
	}
	if _, ok := app.Logger().Handler().(*teeHandler); !ok {
		t.Fatalf("console tee lost after settings reload: %T", app.Logger().Handler())
	}
}

func TestRegisterInstallsBeforeLowerPriorityBootstrapHooks(t *testing.T) {
	t.Setenv(EnvLogLevel, "info")

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
		DefaultDev:      false,
	})
	Register(app)

	var sawTee bool
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		_, sawTee = e.App.Logger().Handler().(*teeHandler)
		return nil
	})

	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	if !sawTee {
		t.Fatal("expected stdout tee before priority-0 OnBootstrap unwind")
	}
}
