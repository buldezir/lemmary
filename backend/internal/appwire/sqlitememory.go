package appwire

import (
	"log/slog"

	"github.com/pocketbase/pocketbase/core"
)

const sqliteShrinkCron = "*/10 * * * *"

// The minute crons keep one connection from ever idling out, so its 32 MB page
// cache stays resident after a busy spell. modernc's SQLite shares one cache
// group process-wide, so this frees every connection's unpinned pages.
func registerSQLiteShrink(app core.App) {
	app.Cron().MustAdd("sqlite_shrink_memory", sqliteShrinkCron, func() {
		shrinkSQLiteMemory(app)
	})
}

func shrinkSQLiteMemory(app core.App) {
	if _, err := app.DB().NewQuery("PRAGMA shrink_memory").Execute(); err != nil {
		app.Logger().Warn("sqlite shrink_memory failed", slog.Any("error", err))
	}
}
