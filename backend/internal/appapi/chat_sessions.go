package appapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/chat"
)

type chatSessionList struct {
	Page       int                `json:"page"`
	PerPage    int                `json:"perPage"`
	TotalItems int                `json:"totalItems"`
	TotalPages int                `json:"totalPages"`
	Items      []chat.SessionInfo `json:"items"`
}

type chatSessionDetail struct {
	Session  chat.SessionInfo   `json:"session"`
	Messages []chat.MessageInfo `json:"messages"`
	// Truncated says the transcript was longer than one response carries.
	// There is no message pagination yet.
	Truncated bool `json:"truncated"`
	// Running says a run is writing into this conversation right now: a turn
	// is stored whole when the run ends, so a chat opened mid-run otherwise
	// reads as empty and finished.
	Running bool `json:"running"`
}

type chatSessionResponse struct {
	Session chat.SessionInfo `json:"session"`
}

type chatRenameRequest struct {
	Title string `json:"title"`
}

// chatListPageSize follows MaxSessionsPerUser rather than the document list's
// 12: the chat rail is a scrolling sidebar, and since an account cannot hold
// more sessions than this, one request is always the whole list. It also bounds
// the document-title lookup below.
const chatListPageSize = chat.MaxSessionsPerUser

func parseChatListQuery(values url.Values, ownerID string) (chat.SessionQuery, int, int, error) {
	page := positiveIntValue(values, "page", 1)
	perPage := positiveIntValue(values, "perPage", chatListPageSize)
	if perPage > chatListPageSize {
		perPage = chatListPageSize
	}

	q := chat.SessionQuery{
		UserID:     ownerID,
		DocumentID: strings.TrimSpace(values.Get("document")),
		Offset:     (page - 1) * perPage,
		Limit:      perPage,
	}

	if raw := strings.TrimSpace(values.Get("kind")); raw != "" {
		kind, ok := chat.ParseKind(raw)
		if !ok {
			return chat.SessionQuery{}, 0, 0, fmt.Errorf("Unknown chat kind: %s.", raw)
		}
		q.Kind = kind
	}

	return q, page, perPage, nil
}

func positiveIntValue(values url.Values, name string, fallback int) int {
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func handleListChats(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		q, page, perPage, err := parseChatListQuery(e.Request.URL.Query(), ownerID)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		records, total, err := chat.ListSessions(app, q)
		if err != nil {
			app.Logger().Error("list chat sessions failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Chats are unavailable.")
		}

		items := make([]chat.SessionInfo, 0, len(records))
		for _, record := range records {
			info := chat.ToSessionInfo(record)
			// One primary-key lookup per row, against a client that would
			// otherwise make the same lookups over HTTP. Skipped on error: the
			// document may be mid-cascade, or belong to another account.
			if info.Document != "" {
				if doc, err := app.FindRecordById("documents", info.Document); err == nil {
					info.DocumentTitle = doc.GetString("title")
				}
			}
			items = append(items, info)
		}

		totalPages := 0
		if perPage > 0 {
			totalPages = (total + perPage - 1) / perPage
		}

		return writeJSON(e, http.StatusOK, chatSessionList{
			Page:       page,
			PerPage:    perPage,
			TotalItems: total,
			TotalPages: totalPages,
			Items:      items,
		})
	}
}

func handleGetChat(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		session, err := ownedChatSession(app, e)
		if err != nil {
			return writeChatOwnerOrSessionError(e, app, err)
		}

		// One over the cap, so a transcript at the boundary can be told apart
		// from one that ran past it.
		records, err := chat.ListMessages(app, session.Id, chat.MaxReplayMessages+1)
		if err != nil {
			app.Logger().Error("read chat transcript failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Chat is unavailable.")
		}
		truncated := len(records) > chat.MaxReplayMessages
		if truncated {
			// Drop the oldest, keep the live end: `truncated` is only honest if
			// that is the side lost.
			records = records[len(records)-chat.MaxReplayMessages:]
		}

		messages := make([]chat.MessageInfo, 0, len(records))
		for _, record := range records {
			messages = append(messages, chat.ToMessageInfo(record))
		}

		info := chat.ToSessionInfo(session)
		if info.Document != "" {
			if doc, err := app.FindRecordById("documents", info.Document); err == nil {
				info.DocumentTitle = doc.GetString("title")
			}
		}

		return writeJSON(e, http.StatusOK, chatSessionDetail{
			Session:   info,
			Messages:  messages,
			Truncated: truncated,
			Running:   sessionRunning(session.Id),
		})
	}
}

func handlePatchChat(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		session, err := ownedChatSession(app, e)
		if err != nil {
			return writeChatOwnerOrSessionError(e, app, err)
		}

		var req chatRenameRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}

		if err := chat.RenameSession(app, session, req.Title); err != nil {
			app.Logger().Error("rename chat failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to rename the chat.")
		}

		return writeJSON(e, http.StatusOK, chatSessionResponse{Session: chat.ToSessionInfo(session)})
	}
}

func handleDeleteChat(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		session, err := ownedChatSession(app, e)
		if err != nil {
			return writeChatOwnerOrSessionError(e, app, err)
		}
		if err := chat.DeleteSession(app, session); err != nil {
			app.Logger().Error("delete chat failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to delete the chat.")
		}
		e.Response.WriteHeader(http.StatusNoContent)
		return nil
	}
}

// ownedChatSession resolves the {id} path value against the caller's account.
func ownedChatSession(app core.App, e *core.RequestEvent) (*core.Record, error) {
	ownerID, err := resolveOwnerUserID(app, e)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(e.Request.PathValue("id"))
	if id == "" {
		return nil, chat.ErrNotFound
	}
	return chat.FindOwnedSession(app, ownerID, id)
}

// writeChatOwnerOrSessionError keeps the owner-resolution failures separate
// from the not-found ones when both can reach the same handler.
func writeChatOwnerOrSessionError(e *core.RequestEvent, app core.App, err error) error {
	var clientErr *ownerClientError
	if errors.As(err, &clientErr) {
		return writeOwnerError(e, err)
	}
	return writeChatSessionError(e, app, err)
}
