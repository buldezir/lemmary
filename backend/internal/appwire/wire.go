package appwire

import (
	"net/http"
	"os"

	"lemmary/backend/internal/appapi"
	"lemmary/backend/internal/authguard"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/ext"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/mailsink"
	"lemmary/backend/internal/ngxapi"
	"lemmary/backend/internal/worker"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// Register wires all application hooks, APIs, and the SPA static handler onto app.
// publicDir is the directory containing the built frontend; indexFallback enables SPA routing.
func Register(app *pocketbase.PocketBase, rt *config.Runtime, publicDir string, indexFallback bool) {
	ed := edition()

	// Read once, here, so every consumer sees the same numbers: the hooks that
	// enforce them, the caps the bulk importers lower to match, and the usage
	// endpoint the UI reads.
	lim, badLimitKeys := limits.FromEnv(app.Logger())
	applyPerFileCaps(lim)

	ft := fulltext.New()
	config.RegisterHooks(app, rt)
	// Installed before anything can trigger a reload: RegisterHooks only binds
	// OnBootstrap, which does not fire until app.Execute runs, well after this
	// function returns.
	rt.SetSnapshotDecorator(ed.DecorateSnapshot)
	authguard.Register(app)
	mailsink.Register(app)
	fulltext.Register(app, ft)
	// Before worker.Register: PocketBase runs equal-priority handlers in
	// registration order, so this is what makes an over-limit upload refused
	// before duplicates.AssignChecksumFromUpload reads the whole file to hash it.
	limits.Register(app, lim)
	appapi.Register(app, rt, ft, lim, badLimitKeys)
	ngxapi.Register(app, ft)
	worker.Register(app, rt, worker.Options{
		ExtraSteps: ed.Steps,
		StepPlans:  ed.StepPlans,
	})

	// The edition registers last so its routes and hooks are bound after every
	// core one. That ordering is what lets it wrap a core route or bind a hook
	// that runs after the core handler, and it cannot be had the other way
	// round.
	for _, register := range ed.Register {
		register(app, ext.Deps{Runtime: rt, FullText: ft})
	}
	if ed.Name != "" {
		app.Logger().Info("edition registered", "edition", ed.Name)
	}

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
