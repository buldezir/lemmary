package appapi

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/mail"
)

func Register(app core.App, rt *config.Runtime) {
	mailSvc := mail.NewService(app, rt)
	mail.RegisterCron(app, mailSvc)

	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: 45,
		Func: func(e *core.ServeEvent) error {
			g := e.Router.Group("/api/app")
			g.GET("/meta", handleGetMeta(app))
			g.GET("/setup/status", handleGetSetupStatus(app, rt))
			g.POST("/setup/admin", handlePostSetupAdmin(app))
			g.POST("/documents/{documentId}/chat", bindAuth(handleDocumentChat(app, rt)))
			g.POST("/search", bindAuth(handleDeepSearch(app, rt)))
			g.GET("/ocr/providers", bindAuth(handleOCRProviders(rt)))
			g.POST("/ocr/test", bindAuth(handleOCRTest(app, rt)))
			g.GET("/settings", bindSuperuser(handleGetSettings(app, rt)))
			g.PATCH("/settings", bindSuperuser(handlePatchSettings(app, rt)))
			registerMailRoutes(g, mailSvc)
			return e.Next()
		},
	})
}
