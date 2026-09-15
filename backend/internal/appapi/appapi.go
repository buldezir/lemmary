package appapi

import (
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/pdfsplit"
	"lemmary/backend/internal/zipimport"
)

// sweeper must be the same instance the worker's cron uses, which is what stops
// a click and a tick from embedding the same documents twice.
func Register(
	app core.App,
	rt *config.Runtime,
	idx *fulltext.Index,
	lim limits.Limits,
	badLimitKeys []string,
	sweeper EmbeddingSweeper,
) {
	RegisterAppName(app)
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: 45,
		Func: func(e *core.ServeEvent) error {
			g := e.Router.Group("/api/app")
			g.GET("/meta", handleGetMeta(app, rt))
			g.GET("/me", handleGetMe(app))
			g.GET("/limits", bindAuth(handleGetLimits(app, lim, badLimitKeys)))
			g.GET("/setup/status", handleGetSetupStatus(app, rt))
			g.POST("/setup/admin", handlePostSetupAdmin(app))
			g.POST("/ensure-user", handlePostEnsureUser(app))
			// The two login routes are public by necessity: the caller has no
			// session yet.
			g.POST("/passkeys/login/begin", handlePostPasskeyLoginBegin(app))
			g.POST("/passkeys/login/finish", handlePostPasskeyLoginFinish(app)).
				Bind(apis.BodyLimit(passkeyMaxBodyBytes))
			g.GET("/passkeys", bindAuth(handleGetPasskeys(app)))
			g.POST("/passkeys/register/begin", bindAuth(handlePostPasskeyRegisterBegin(app)))
			g.POST("/passkeys/register/finish", bindAuth(handlePostPasskeyRegisterFinish(app))).
				Bind(apis.BodyLimit(passkeyMaxBodyBytes))
			g.PATCH("/passkeys/{id}", bindAuth(handlePatchPasskey(app)))
			g.DELETE("/passkeys/{id}", bindAuth(handleDeletePasskey(app)))
			g.POST("/documents/{documentId}/chat", bindAuth(handleDocumentChat(app, rt))).
				Bind(apis.BodyLimit(chatMaxBodyBytes))
			g.GET("/documents/export", bindAuth(handleExportDocuments(app)))
			g.GET("/documents/search", bindAuth(handleDocumentSearch(app, idx)))
			g.GET("/documents/timeline", bindAuth(handleDocumentsTimeline(app)))
			g.POST("/documents/reprocess-failed", bindAuth(handlePostReprocessFailed(app, rt)))
			// The way back out of a mistaken import: stop what is queued, then
			// throw away what it was queued for.
			g.POST("/documents/discard-unprocessed", bindAuth(handlePostDiscardUnprocessed(app)))
			g.POST("/jobs/stop", bindAuth(handlePostStopQueue(app)))
			g.POST("/search", bindAuth(handleDeepSearch(app, rt, idx))).
				Bind(apis.BodyLimit(chatMaxBodyBytes))
			g.POST("/search/stream", bindAuth(handleSearchStream(app, rt, idx))).
				Bind(apis.BodyLimit(chatMaxBodyBytes))
			g.POST("/search/cancel", bindAuth(handleSearchCancel(app)))
			g.POST("/search/reindex", bindAdmin(handleSearchReindex(app, idx)))
			// The chat collections carry no API rules, so this is their only
			// access path.
			g.GET("/chats", bindAuth(handleListChats(app)))
			g.GET("/chats/{id}", bindAuth(handleGetChat(app)))
			g.PATCH("/chats/{id}", bindAuth(handlePatchChat(app)))
			g.DELETE("/chats/{id}", bindAuth(handleDeleteChat(app)))
			g.POST("/chats/{id}/fork", bindAuth(handlePostForkChat(app)))
			// The OCR test page sends no purpose and means OCR.
			g.GET("/ocr/providers", bindAuth(handlePickableProviders(app, rt, aiprovider.PurposeOCR)))
			// Auth rather than admin: an override is a per-user choice among
			// providers an admin configured, and this answer carries no
			// credential.
			g.GET("/ai/providers", bindAuth(handlePickableProviders(app, rt, aiprovider.PurposeLLM)))
			// Without a route-level limit the multipart parse consumes the whole
			// request under PocketBase's 32MB default before the handler's own
			// check can reject it.
			g.POST("/ocr/test", bindAuth(handleOCRTest(app, rt))).
				Bind(apis.BodyLimit(ocrTestMaxFileBytes + (1 << 20)))
			g.GET("/settings", bindAdmin(handleGetSettings(app, rt)))
			g.PATCH("/settings", bindAdmin(handlePatchSettings(app, rt)))
			g.GET("/settings/embeddings", bindAdmin(handleGetEmbeddingStats(app, rt)))
			g.GET("/embeddings/backfill", bindAdmin(handleGetEmbeddingBackfill(app, rt, sweeper)))
			g.POST("/embeddings/backfill", bindAdmin(handlePostEmbeddingBackfill(app, rt, sweeper)))
			g.GET("/providers", bindAdmin(handleListProviders(app)))
			g.POST("/providers", bindAdmin(handleCreateProvider(app, rt)))
			g.PATCH("/providers/{id}", bindAdmin(handlePatchProvider(app, rt)))
			g.DELETE("/providers/{id}", bindAdmin(handleDeleteProvider(app, rt)))
			// Auth rather than admin, unlike the rest of /providers: the answer is
			// model ids and an SDK name, no key, account or base URL.
			g.GET("/providers/{id}/models", bindAuth(handleListProviderModels(app)))
			// Two calls rather than one blocking handler: the browser owns the
			// polling interval.
			g.POST("/providers/{id}/chatgpt/device", bindAdmin(handleChatGPTDeviceStart(app, rt)))
			g.POST("/providers/{id}/chatgpt/device/poll", bindAdmin(handleChatGPTDevicePoll(app, rt)))
			g.DELETE("/providers/{id}/chatgpt", bindAdmin(handleChatGPTSignOut(app, rt)))
			g.POST("/duplicates/scan", bindAdmin(handlePostDuplicatesScan(app, rt)))
			g.POST("/taxonomy/prune", bindAdmin(handlePostTaxonomyPrune(app)))
			// Auth rather than admin: tags are user-owned, and a run spends
			// only on the caller's own documents.
			g.GET("/tags/{tagId}/assign", bindAuth(handleGetTagAssignPreview(app)))
			g.POST("/tags/{tagId}/assign", bindAuth(handlePostTagAssign(app, rt)))
			g.GET("/tags/assign/status", bindAuth(handleGetTagAssignStatus(app)))
			g.POST("/import/ngx", bindAuth(handlePostImportNgx(app)))
			g.GET("/import/ngx/status", bindAuth(handleGetImportNgxStatus(app)))
			// Two path families over one implementation: only the upload route
			// says which source it is, and the staged upload remembers.
			g.POST("/import/amazon/upload", bindAuth(handlePostImportUpload(app, lim, zipimport.SourceAmazon))).
				Bind(apis.BodyLimit(config.StagingMaxBytesFromEnv()))
			g.DELETE("/import/amazon/upload", bindAuth(handleDeleteImportUpload(app)))
			g.POST("/import/amazon", bindAuth(handlePostImport(app)))
			g.GET("/import/amazon/status", bindAuth(handleGetImportStatus(app)))
			g.POST("/import/zip/upload", bindAuth(handlePostImportUpload(app, lim, zipimport.SourceFiles))).
				Bind(apis.BodyLimit(config.StagingMaxBytesFromEnv()))
			g.DELETE("/import/zip/upload", bindAuth(handleDeleteImportUpload(app)))
			g.POST("/import/zip", bindAuth(handlePostImport(app)))
			g.GET("/import/zip/status", bindAuth(handleGetImportStatus(app)))
			g.POST("/import/archive/upload", bindAuth(handlePostImportArchiveUpload(app, lim))).
				Bind(apis.BodyLimit(config.StagingMaxBytesFromEnv()))
			g.DELETE("/import/archive/upload", bindAuth(handleDeleteImportArchiveUpload(app)))
			g.POST("/import/archive", bindAuth(handlePostImportArchive(app)))
			g.GET("/import/archive/status", bindAuth(handleGetImportArchiveStatus(app)))
			// The extra megabyte is headroom for the multipart framing, so a PDF
			// at the cap reaches the handler and gets the message that explains
			// the limit instead of a bare 413.
			g.POST("/split/upload", bindAuth(handlePostSplitUpload(app))).
				Bind(apis.BodyLimit(pdfsplit.MaxPDFBytes + (1 << 20)))
			g.DELETE("/split/upload", bindAuth(handleDeleteSplitUpload(app)))
			g.GET("/split/page", bindAuth(handleGetSplitPage(app)))
			g.POST("/split/detect", bindAuth(handlePostSplitDetect(app, rt)))
			g.GET("/split/detect/status", bindAuth(handleGetSplitDetectStatus(app)))
			g.POST("/split", bindAuth(handlePostSplit(app, lim)))
			g.GET("/split/status", bindAuth(handleGetSplitStatus(app)))
			// A scan is a job rather than a synchronous call: a feeder run is
			// minutes long, and a proxy's read timeout would cut it in half.
			g.GET("/scan/discover", bindAuth(handleGetScanDiscover(app)))
			g.POST("/scan", bindAuth(handlePostScan(app, lim)))
			g.GET("/scan/status", bindAuth(handleGetScanStatus(app)))
			g.GET("/scan/pdf", bindAuth(handleGetScanPDF(app)))
			g.DELETE("/scan", bindAuth(handleDeleteScan(app)))
			g.POST("/scan/document", bindAuth(handlePostScanDocument(app)))
			return e.Next()
		},
	})
}
