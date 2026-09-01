package appwire

import (
	"net/http"
	"os"

	"lemmary/backend/internal/appapi"
	"lemmary/backend/internal/authguard"
	"lemmary/backend/internal/config"
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
	// Read once, here, so every consumer sees the same numbers: the hooks that
	// enforce them, the caps the bulk importers lower to match, and the usage
	// endpoint the UI reads.
	lim, badLimitKeys := limits.FromEnv(app.Logger())
	applyPerFileCaps(lim)

	ft := fulltext.New()
	config.RegisterHooks(app, rt)
	authguard.Register(app)
	mailsink.Register(app)
	fulltext.Register(app, ft)
	// Before worker.Register: PocketBase runs equal-priority handlers in
	// registration order, so this is what makes an over-limit upload refused
	// before duplicates.AssignChecksumFromUpload reads the whole file to hash it.
	// The same ordering now carries limits.MaxOCRPages, which binds on every
	// install and not only where a plan limit is set.
	limits.Register(app, lim)
	appapi.Register(app, rt, ft, lim, badLimitKeys)
	ngxapi.Register(app, ft)
	worker.Register(app, rt)

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
