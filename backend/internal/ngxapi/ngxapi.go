package ngxapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"paperless-go/backend/internal/fulltext"
)

// Register mounts paperless-ngx compatible REST endpoints on the PocketBase router.
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

			g.GET("/schema/", handleSchema)
			g.GET("/schema", handleSchema)

			g.GET("/profile/", bindAuth(handleProfile))
			g.PATCH("/profile/", bindAuth(handleProfile))
			g.GET("/profile", bindAuth(handleProfile))

			g.GET("/ui_settings/", bindAuth(handleUiSettings))
			g.POST("/ui_settings/", bindAuth(handleUiSettings))
			g.GET("/ui_settings", bindAuth(handleUiSettings))
			g.POST("/ui_settings", bindAuth(handleUiSettings))

			g.GET("/config/", bindAuth(handleAppConfig))
			g.GET("/config", bindAuth(handleAppConfig))
			g.GET("/remote_version/", bindAuth(handleRemoteVersion))
			g.GET("/remote_version", bindAuth(handleRemoteVersion))

			registerEmptyListRoutes(g, "/custom_fields")
			registerEmptyListRoutes(g, "/saved_views")
			registerEmptyListRoutes(g, "/storage_paths")
			registerEmptyListRoutes(g, "/users")
			registerEmptyListRoutes(g, "/groups")

			g.GET("/", handleAPIRoot)

			g.POST("/token/", handleToken)
			g.POST("/token", handleToken)
			g.GET("/token/", handleTokenMethodNotAllowed)
			g.GET("/token", handleTokenMethodNotAllowed)
			g.HEAD("/token/", handleTokenMethodNotAllowed)
			g.HEAD("/token", handleTokenMethodNotAllowed)

			registerDocumentRoutes(g, idx)
			registerTaxonomyRoutes(g)

			g.GET("/tasks/", bindAuth(handleListTasks))
			g.GET("/tasks", bindAuth(handleListTasks))

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
	for _, r := range list {
		g.GET(r.list, bindAuth(handleListDocuments(idx)))
		g.GET(r.item, bindAuth(handleGetDocument))
		g.PATCH(r.item, bindAuth(handlePatchDocument))
		g.DELETE(r.item, bindAuth(handleDeleteDocument))
		g.GET(r.itemDownload, bindAuth(handleDownloadDocument))
		g.GET(r.itemThumb, bindAuth(handleDocumentThumb))
		g.POST(r.postDocument, bindAuth(handlePostDocument))
	}
}

// namedEntityRoutes is the CRUD handler set for one paperless-ngx taxonomy
// collection. All three collections expose the same five endpoints.
type namedEntityRoutes struct {
	base                          string
	list, create, get, patch, del func(*core.RequestEvent) error
}

func registerNamedEntityRoutes(g *router.RouterGroup[*core.RequestEvent], routes namedEntityRoutes) {
	for _, base := range []string{routes.base + "/", routes.base} {
		g.GET(base, bindAuth(routes.list))
		g.POST(base, bindAuth(routes.create))
		item := itemPath(base, "{id}")
		g.GET(item, bindAuth(routes.get))
		g.PATCH(item, bindAuth(routes.patch))
		g.DELETE(item, bindAuth(routes.del))
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
		g.GET(path, bindAuth(handleEmptyList))
	}
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
