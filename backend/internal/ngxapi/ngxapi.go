package ngxapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"lemmary/backend/internal/fulltext"
)

func Register(app core.App, idx *fulltext.Index) {
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: 40,
		Func: func(e *core.ServeEvent) error {
			// paperless-ngx clients send "Authorization: Token <jwt>"; PocketBase only strips "Bearer ".
			e.Router.Bind(&hook.Handler[*core.RequestEvent]{
				Priority: -1030,
				Func:     normalizePaperlessAuthHeader,
			})
			return e.Next()
		},
	})

	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: 50,
		Func: func(e *core.ServeEvent) error {
			g := e.Router.Group("/api")

			g.GET(exact("/schema/"), handleSchema)
			g.GET(exact("/schema"), handleSchema)

			g.GET(exact("/profile/"), bindAuth(handleProfile))
			g.PATCH(exact("/profile/"), bindAuth(handleProfile))
			g.GET(exact("/profile"), bindAuth(handleProfile))
			g.POST(exact("/profile/generate_auth_token/"), bindAuth(handleGenerateAuthToken))
			g.POST(exact("/profile/generate_auth_token"), bindAuth(handleGenerateAuthToken))

			g.GET(exact("/ui_settings/"), bindAuth(handleUiSettings))
			g.POST(exact("/ui_settings/"), bindAuth(handleUiSettings))
			g.GET(exact("/ui_settings"), bindAuth(handleUiSettings))
			g.POST(exact("/ui_settings"), bindAuth(handleUiSettings))

			g.GET(exact("/config/"), bindAuth(handleAppConfig))
			g.GET(exact("/config"), bindAuth(handleAppConfig))
			// Public, as in paperless-ngx: clients probe it before signing in.
			g.GET(exact("/remote_version/"), handleRemoteVersion)
			g.GET(exact("/remote_version"), handleRemoteVersion)

			registerEmptyListRoutes(g, "/custom_fields")
			registerEmptyListRoutes(g, "/saved_views")
			registerEmptyListRoutes(g, "/storage_paths")
			registerEmptyListRoutes(g, "/users")
			registerEmptyListRoutes(g, "/groups")

			g.GET(exact("/"), handleAPIRoot)
			g.GET("/{path...}", handleAPINotFound)

			g.POST(exact("/token/"), handleToken)
			g.POST(exact("/token"), handleToken)
			g.GET(exact("/token/"), handleTokenMethodNotAllowed)
			g.GET(exact("/token"), handleTokenMethodNotAllowed)
			g.HEAD(exact("/token/"), handleTokenMethodNotAllowed)
			g.HEAD(exact("/token"), handleTokenMethodNotAllowed)

			registerDocumentRoutes(g, idx)
			registerTaxonomyRoutes(g)

			g.GET(exact("/tasks/"), bindAuth(handleListTasks))
			g.GET(exact("/tasks"), bindAuth(handleListTasks))
			for _, path := range []string{"/tasks/active/", "/tasks/active"} {
				g.GET(exact(path), bindAuth(handleActiveTasks))
			}
			for _, path := range []string{"/tasks/status_counts/", "/tasks/status_counts"} {
				g.GET(exact(path), bindAuth(handleTaskStatusCounts))
			}
			for _, path := range []string{
				"/acknowledge_tasks/", "/acknowledge_tasks",
				"/tasks/acknowledge/", "/tasks/acknowledge",
			} {
				g.POST(exact(path), bindAuth(handleAcknowledgeTasks))
			}

			return e.Next()
		},
	})
}

func registerDocumentRoutes(g *router.RouterGroup[*core.RequestEvent], idx *fulltext.Index) {
	list := []struct {
		list, item, itemDownload, itemThumb, postDocument string
	}{
		{"/documents/", "/documents/{id}/", "/documents/{id}/download/", "/documents/{id}/thumb/", "/documents/post_document/"},
		{"/documents", "/documents/{id}", "/documents/{id}/download", "/documents/{id}/thumb", "/documents/post_document"},
	}
	for _, path := range []string{"/documents/{id}/preview/", "/documents/{id}/preview"} {
		g.GET(exact(path), bindAuth(handleDownloadDocument))
	}
	for _, path := range []string{"/documents/{id}/suggestions/", "/documents/{id}/suggestions"} {
		g.GET(exact(path), bindAuth(handleDocumentSuggestions))
	}
	for _, r := range list {
		g.GET(exact(r.list), bindAuth(handleListDocuments(idx)))
		g.GET(exact(r.item), bindAuth(handleGetDocument))
		g.PATCH(exact(r.item), bindAuth(handlePatchDocument))
		g.DELETE(exact(r.item), bindAuth(handleDeleteDocument))
		g.GET(exact(r.itemDownload), bindAuth(handleDownloadDocument))
		g.GET(exact(r.itemThumb), bindAuth(handleDocumentThumb))
		g.POST(exact(r.postDocument), bindAuth(handlePostDocument))
	}
}

type namedEntityRoutes struct {
	base                          string
	list, create, get, patch, del func(*core.RequestEvent) error
}

func registerNamedEntityRoutes(g *router.RouterGroup[*core.RequestEvent], routes namedEntityRoutes) {
	for _, base := range []string{routes.base + "/", routes.base} {
		g.GET(exact(base), bindAuth(routes.list))
		g.POST(exact(base), bindAuth(routes.create))
		item := itemPath(base, "{id}")
		g.GET(exact(item), bindAuth(routes.get))
		g.PATCH(exact(item), bindAuth(routes.patch))
		g.DELETE(exact(item), bindAuth(routes.del))
	}
}

func registerTaxonomyRoutes(g *router.RouterGroup[*core.RequestEvent]) {
	for _, routes := range []namedEntityRoutes{
		{
			base:   "/tags",
			list:   handleListTags,
			create: handleCreateTag,
			get:    handleGetTag,
			patch:  handlePatchTag,
			del:    handleDeleteTag,
		},
		{
			base:   "/correspondents",
			list:   handleListCorrespondents,
			create: handleCreateCorrespondent,
			get:    handleGetCorrespondent,
			patch:  handlePatchCorrespondent,
			del:    handleDeleteCorrespondent,
		},
		{
			base:   "/document_types",
			list:   handleListDocumentTypes,
			create: handleCreateDocumentType,
			get:    handleGetDocumentType,
			patch:  handlePatchDocumentType,
			del:    handleDeleteDocumentType,
		},
	} {
		registerNamedEntityRoutes(g, routes)
	}
}

func registerEmptyListRoutes(g *router.RouterGroup[*core.RequestEvent], base string) {
	withSlash := base + "/"
	withoutSlash := base
	for _, path := range []string{withSlash, withoutSlash} {
		g.GET(exact(path), bindAuth(handleEmptyList))
	}
}

// exact pins a path to itself. Go's mux reads a pattern ending in "/" as the
// whole subtree, so /documents/{id}/ also answered /documents/{id}/suggestions/
// with the document, and a client decoded it as something else entirely.
func exact(path string) string {
	if strings.HasSuffix(path, "/") {
		return path + "{$}"
	}
	return path
}

// handleAPINotFound answers what paperless-ngx would 404. Without it an
// unknown path falls through to the web app and comes back as a 200 page.
func handleAPINotFound(e *core.RequestEvent) error {
	return notFound(e, "Not found.")
}

func itemPath(base, segment string) string {
	if strings.HasSuffix(base, "/") {
		return base + segment + "/"
	}
	return base + "/" + segment
}

func handleAPIRoot(e *core.RequestEvent) error {
	if err := checkAPIVersion(e); err != nil {
		return err
	}
	return e.Redirect(http.StatusFound, "/api/schema/")
}
