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
)

// sweeper is the embedding backfill the Management page starts by hand. It is
// the same instance the worker's cron uses, which is what stops a click and a
// tick from embedding the same documents twice.
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
			// Passkey sign-in. The two login routes are public by necessity: the
			// caller has no session yet, which is the whole point.
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
			g.POST("/search", bindAuth(handleDeepSearch(app, rt, idx))).
				Bind(apis.BodyLimit(chatMaxBodyBytes))
			g.POST("/search/stream", bindAuth(handleSearchStream(app, rt, idx))).
				Bind(apis.BodyLimit(chatMaxBodyBytes))
			g.POST("/search/cancel", bindAuth(handleSearchCancel(app)))
			g.POST("/search/reindex", bindAdmin(handleSearchReindex(app, idx)))
			// Saved conversations behind both AI chat surfaces. The collections
			// carry no API rules, so this is their only access path.
			g.GET("/chats", bindAuth(handleListChats(app)))
			g.GET("/chats/{id}", bindAuth(handleGetChat(app)))
			g.PATCH("/chats/{id}", bindAuth(handlePatchChat(app)))
			g.DELETE("/chats/{id}", bindAuth(handleDeleteChat(app)))
			// The OCR test page sends no purpose and means OCR.
			g.GET("/ocr/providers", bindAuth(handlePickableProviders(app, rt, aiprovider.PurposeOCR)))
			// The same list for any purpose, for the model pickers on a chat and
			// on a reprocess job. Auth rather than admin: an override is a
			// per-user choice among providers an admin already configured, and
			// this answer carries no credential -- see pickableProvider.
			g.GET("/ai/providers", bindAuth(handlePickableProviders(app, rt, aiprovider.PurposeLLM)))
			// Without a route-level limit the multipart parse consumes the whole
			// request under PocketBase's 32MB default before the handler's own
			// 10MB check can reject it.
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
			// Auth rather than admin, unlike the rest of /providers: a model picker
			// is useless without the catalogue, and the answer is model ids and an
			// SDK name -- no key, no account, no base URL.
			g.GET("/providers/{id}/models", bindAuth(handleListProviderModels(app)))
			// Device-code sign-in for the chatgpt SDK. Two calls rather than
			// one blocking handler: the browser owns the polling interval.
			g.POST("/providers/{id}/chatgpt/device", bindAdmin(handleChatGPTDeviceStart(app, rt)))
			g.POST("/providers/{id}/chatgpt/device/poll", bindAdmin(handleChatGPTDevicePoll(app, rt)))
			g.DELETE("/providers/{id}/chatgpt", bindAdmin(handleChatGPTSignOut(app, rt)))
			g.POST("/duplicates/scan", bindAdmin(handlePostDuplicatesScan(app, rt)))
			g.POST("/taxonomy/prune", bindAdmin(handlePostTaxonomyPrune(app)))
			g.POST("/import/ngx", bindAuth(handlePostImportNgx(app)))
			g.GET("/import/ngx/status", bindAuth(handleGetImportNgxStatus(app)))
			g.POST("/import/amazon/upload", bindAuth(handlePostImportAmazonUpload(app, lim))).
				Bind(apis.BodyLimit(config.StagingMaxBytesFromEnv()))
			g.DELETE("/import/amazon/upload", bindAuth(handleDeleteImportAmazonUpload(app)))
			g.POST("/import/amazon", bindAuth(handlePostImportAmazon(app)))
			g.GET("/import/amazon/status", bindAuth(handleGetImportAmazonStatus(app)))
			g.POST("/import/archive/upload", bindAuth(handlePostImportArchiveUpload(app, lim))).
				Bind(apis.BodyLimit(config.StagingMaxBytesFromEnv()))
			g.DELETE("/import/archive/upload", bindAuth(handleDeleteImportArchiveUpload(app)))
			g.POST("/import/archive", bindAuth(handlePostImportArchive(app)))
			g.GET("/import/archive/status", bindAuth(handleGetImportArchiveStatus(app)))
			// The extra megabyte is headroom for the multipart framing, so a PDF
			// at the cap still reaches the handler and is rejected with the
			// message that explains the limit instead of a bare 413.
			g.POST("/split/upload", bindAuth(handlePostSplitUpload(app))).
				Bind(apis.BodyLimit(pdfsplit.MaxPDFBytes + (1 << 20)))
			g.DELETE("/split/upload", bindAuth(handleDeleteSplitUpload(app)))
			g.GET("/split/page", bindAuth(handleGetSplitPage(app)))
			g.POST("/split/detect", bindAuth(handlePostSplitDetect(app, rt)))
			g.GET("/split/detect/status", bindAuth(handleGetSplitDetectStatus(app)))
			g.POST("/split", bindAuth(handlePostSplit(app, lim)))
			g.GET("/split/status", bindAuth(handleGetSplitStatus(app)))
			// Scanning from an eSCL device on the LAN. The scan itself is a job
			// rather than a synchronous call: a feeder run is minutes long, and
			// a reverse proxy's read timeout would cut it in half.
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
