package appapi

import (
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"paperless-go/backend/internal/amazonimport"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/fulltext"
	"paperless-go/backend/internal/pdfsplit"
)

func Register(app core.App, rt *config.Runtime, idx *fulltext.Index) {
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
			g.GET("/documents/export", bindAuth(handleExportDocuments(app)))
			g.GET("/documents/search", bindAuth(handleDocumentSearch(app, idx)))
			g.POST("/documents/reprocess-failed", bindAuth(handlePostReprocessFailed(app)))
			g.POST("/search", bindAuth(handleDeepSearch(app, rt, idx)))
			g.POST("/search/reindex", bindAdmin(handleSearchReindex(app, idx)))
			g.GET("/ocr/providers", bindAuth(handleOCRProviders(app, rt)))
			// Without a route-level limit the multipart parse consumes the whole
			// request under PocketBase's 32MB default before the handler's own
			// 10MB check can reject it.
			g.POST("/ocr/test", bindAuth(handleOCRTest(app, rt))).
				Bind(apis.BodyLimit(ocrTestMaxFileBytes + (1 << 20)))
			g.GET("/settings", bindAdmin(handleGetSettings(app, rt)))
			g.PATCH("/settings", bindAdmin(handlePatchSettings(app, rt)))
			g.GET("/providers", bindAdmin(handleListProviders(app)))
			g.POST("/providers", bindAdmin(handleCreateProvider(app)))
			g.PATCH("/providers/{id}", bindAdmin(handlePatchProvider(app)))
			g.DELETE("/providers/{id}", bindAdmin(handleDeleteProvider(app)))
			g.GET("/providers/{id}/models", bindAdmin(handleListProviderModels(app)))
			g.POST("/duplicates/scan", bindAdmin(handlePostDuplicatesScan(app, rt)))
			g.POST("/taxonomy/prune", bindAdmin(handlePostTaxonomyPrune(app)))
			g.POST("/import/ngx", bindAuth(handlePostImportNgx(app)))
			g.GET("/import/ngx/status", bindAuth(handleGetImportNgxStatus(app)))
			g.POST("/import/amazon/upload", bindAuth(handlePostImportAmazonUpload(app))).
				Bind(apis.BodyLimit(amazonimport.MaxArchiveBytes))
			g.DELETE("/import/amazon/upload", bindAuth(handleDeleteImportAmazonUpload(app)))
			g.POST("/import/amazon", bindAuth(handlePostImportAmazon(app)))
			g.GET("/import/amazon/status", bindAuth(handleGetImportAmazonStatus(app)))
			g.POST("/split/upload", bindAuth(handlePostSplitUpload(app))).
				Bind(apis.BodyLimit(pdfsplit.MaxPDFBytes))
			g.DELETE("/split/upload", bindAuth(handleDeleteSplitUpload(app)))
			g.GET("/split/page", bindAuth(handleGetSplitPage(app)))
			g.POST("/split/detect", bindAuth(handlePostSplitDetect(app, rt)))
			g.GET("/split/detect/status", bindAuth(handleGetSplitDetectStatus(app)))
			g.POST("/split", bindAuth(handlePostSplit(app)))
			g.GET("/split/status", bindAuth(handleGetSplitStatus(app)))
			return e.Next()
		},
	})
}
