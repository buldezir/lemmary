package appapi

import (
	"log/slog"
	"mime"
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chat"
	"lemmary/backend/internal/report"
)

// handleGetChatReport serves a PDF report a research turn wrote. No file is
// stored: the report is found again in the conversation's own tool calls and
// laid out on every download, so it is deleted, forked and encrypted with the
// chat, by the code that already does that for the chat.
func handleGetChatReport(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		session, err := ownedChatSession(app, e)
		if err != nil {
			return writeChatOwnerOrSessionError(e, app, err)
		}
		thread, err := chat.Thread(app, session.Id)
		if err != nil {
			app.Logger().Error("read chat thread failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Chat is unavailable.")
		}
		made := ai.ThreadArtifacts(thread)
		doc, ok := made.PDFs[e.Request.PathValue("report")]
		if !ok {
			return writeError(e, http.StatusNotFound, "Report not found.")
		}
		out, err := report.Render(doc, made.Charts)
		if err != nil {
			app.Logger().Error("render report failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "The report could not be created.")
		}
		e.Response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": doc.Title + ".pdf"}))
		return e.Blob(http.StatusOK, "application/pdf", out)
	}
}
