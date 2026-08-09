package appapi

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"paperless-go/backend/internal/config"
)

func Register(app core.App, rt *config.Runtime) {
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: 45,
		Func: func(e *core.ServeEvent) error {
			g := e.Router.Group("/api/app")
			g.GET("/meta", handleGetMeta(app))
			g.GET("/me", handleGetMe(app))
			g.GET("/setup/status", handleGetSetupStatus(app, rt))
			g.POST("/setup/admin", handlePostSetupAdmin(app))
			g.POST("/ensure-user", handlePostEnsureUser(app))
			g.POST("/documents/{documentId}/chat", bindAuth(handleDocumentChat(app, rt)))
			g.POST("/search", bindAuth(handleDeepSearch(app, rt)))
			g.GET("/ocr/providers", bindAuth(handleOCRProviders(rt)))
			g.POST("/ocr/test", bindAuth(handleOCRTest(app, rt)))
			g.GET("/settings", bindAdmin(handleGetSettings(app, rt)))
			g.PATCH("/settings", bindAdmin(handlePatchSettings(app, rt)))
			g.POST("/duplicates/scan", bindAdmin(handlePostDuplicatesScan(app, rt)))
			return e.Next()
		},
	})
}
