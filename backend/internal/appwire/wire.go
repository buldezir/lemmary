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
	"lemmary/backend/internal/metrics"
	"lemmary/backend/internal/ngxapi"
	"lemmary/backend/internal/ngxid"
	"lemmary/backend/internal/worker"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// Register wires the application hooks, APIs and the SPA static handler onto
// app. publicDir holds the built frontend; indexFallback enables SPA routing.
func Register(app *pocketbase.PocketBase, rt *config.Runtime, publicDir string, indexFallback bool) {
	// Read once so the enforcing hooks, the importer caps and the usage endpoint
	// all see the same numbers.
	lim, badLimitKeys := limits.FromEnv(app.Logger())
	applyPerFileCaps(lim)
	// Bound to the same numbers, so what a dashboard reports and what the app
	// enforces cannot drift apart. Registered on every install, limits or none:
	// how many documents and pages there are is worth knowing either way.
	registerUsageMetrics(app, lim)

	ft := fulltext.New()
	// The chunk index is derived from the embedding store, so it gets its source
	// before anything can open it. Both are process-wide, like the index.
	ft.SetChunkSource(embed.NewChunkSource())
	embedstore.SetListener(ft)
	// The dimension count is unknown until a provider has answered once, so the
	// binding can change at runtime and every reload re-points the index.
	rt.OnReload(func(reloadApp core.App, snap config.Snapshot) {
		if err := ft.SetVectorSpec(embed.SpecFrom(snap.Cfg)); err != nil {
			reloadApp.Logger().Error("chunk index reconfigure failed", "error", err)
		}
		ft.EnqueueChunkRebuild(reloadApp)
	})
	config.RegisterHooks(app, rt)
	// Before anything that creates records: paperless-ngx addresses rows by an
	// integer id stored on them, and an unstamped record is invisible to it.
	ngxid.Register(app)
	authguard.Register(app)
	mailsink.Register(app)
	fulltext.Register(app, ft)
	// Before worker.Register: equal-priority handlers run in registration order,
	// so an over-limit upload is refused before AssignChecksumFromUpload reads
	// the whole file to hash it. limits.MaxOCRPages rides the same ordering.
	limits.Register(app, lim)
	// Keeps chunk vectors in step: a deleted document takes its rows with it, an
	// edited one is marked stale for the backfill.
	embedstore.Register(app)
	// One backfiller for the worker cron and the manual API sweep: two instances
	// would each think they had the backlog to themselves.
	backfill := worker.NewBackfiller(app, rt)
	appapi.Register(app, rt, ft, lim, badLimitKeys, backfill)
	// After config.RegisterHooks, so the settings singleton and env-seeded
	// providers exist by the time an account is minted.
	appapi.RegisterAdminBootstrap(app)
	appapi.RegisterMCP(app, rt, ft)
	ngxapi.Register(app, ft)
	worker.Register(app, rt, backfill, config.WorkerConcurrencyFromEnv())

	// After worker.Register, which declares the queue gauge. Order is not
	// actually load-bearing -- OpenTelemetry's global meter hands every
	// instrument declared before this point to the provider once it exists --
	// but reading it in wiring order is worth more than saving a line.
	metrics.Register(app)

	registerSQLiteShrink(app)
	registerCOOPHeader(app)

	// Prefer the in-app setup wizard over PocketBase's browser installer UI.
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: -10000,
		Func: func(e *core.ServeEvent) error {
			e.InstallerFunc = nil
			return e.Next()
		},
	})

	registerRequestMetrics(app)

	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			if !e.Router.HasRoute(http.MethodGet, "/{path...}") {
				e.Router.GET("/{path...}", staticWithCacheControl(os.DirFS(publicDir), indexFallback))
			}
			return e.Next()
		},
		Priority: 999,
	})
}
