package appwire

import (
	"net/http"
	"os"

	"lemmary/backend/internal/appapi"
	"lemmary/backend/internal/authguard"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/embed"
	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/mailsink"
	"lemmary/backend/internal/ngxapi"
	"lemmary/backend/internal/ngxid"
	"lemmary/backend/internal/worker"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// Register wires all application hooks, APIs, and the SPA static handler onto app.
// publicDir is the directory containing the built frontend; indexFallback enables SPA routing.
func Register(app *pocketbase.PocketBase, rt *config.Runtime, publicDir string, indexFallback bool) {
	// Read once, here, so every consumer sees the same numbers: the hooks that
	// enforce them, the caps the bulk importers lower to match, and the usage
	// endpoint the UI reads.
	lim, badLimitKeys := limits.FromEnv(app.Logger())
	applyPerFileCaps(lim)

	ft := fulltext.New()
	// The chunk index is derived from the embedding store, so it is given its
	// source before anything can open it, and the store is told where to send
	// its change notifications. Both are process-wide, like the index itself.
	ft.SetChunkSource(embed.NewChunkSource())
	embedstore.SetListener(ft)
	// The dimension count is not known until a provider has answered once, so
	// the binding the chunk index is built for can change at runtime; every
	// reload re-points the index and schedules the fill in the background.
	rt.OnReload(func(reloadApp core.App, snap config.Snapshot) {
		if err := ft.SetVectorSpec(embed.SpecFrom(snap.Cfg)); err != nil {
			reloadApp.Logger().Error("chunk index reconfigure failed", "error", err)
		}
		ft.EnqueueChunkRebuild(reloadApp)
	})
	config.RegisterHooks(app, rt)
	// Before anything that creates records: the paperless-ngx API addresses
	// documents and taxonomy by an integer id stored on the row, and a record
	// that slipped in unstamped would be invisible to every paperless client.
	ngxid.Register(app)
	authguard.Register(app)
	mailsink.Register(app)
	fulltext.Register(app, ft)
	// Before worker.Register: PocketBase runs equal-priority handlers in
	// registration order, so this is what makes an over-limit upload refused
	// before duplicates.AssignChecksumFromUpload reads the whole file to hash it.
	// The same ordering now carries limits.MaxOCRPages, which binds on every
	// install and not only where a plan limit is set.
	limits.Register(app, lim)
	// Keeps the chunk vectors in step with the documents they describe: a
	// deleted document takes its rows with it, an edited one is marked stale
	// for the backfill.
	embedstore.Register(app)
	// One backfiller for both callers: the worker's cron below and the manual
	// sweep the API exposes. It is built here because appapi binds its routes
	// before worker.Register runs, and two instances would each think they had
	// the backlog to themselves.
	backfill := worker.NewBackfiller(app, rt)
	appapi.Register(app, rt, ft, lim, badLimitKeys, backfill)
	// After config.RegisterHooks so the settings singleton and any env-seeded
	// providers exist by the time an account is minted: an instance that hands
	// somebody a login should have somewhere for them to land.
	appapi.RegisterAdminBootstrap(app)
	ngxapi.Register(app, ft)
	worker.Register(app, rt, backfill)

	registerCOOPHeader(app)

	// Prefer the in-app setup wizard over PocketBase's browser installer UI.
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: -10000,
		Func: func(e *core.ServeEvent) error {
			e.InstallerFunc = nil
			return e.Next()
		},
	})

	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			if !e.Router.HasRoute(http.MethodGet, "/{path...}") {
				e.Router.GET("/{path...}", apis.Static(os.DirFS(publicDir), indexFallback))
			}
			return e.Next()
		},
		Priority: 999,
	})
}
