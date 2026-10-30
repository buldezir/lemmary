package appapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chat"
	"lemmary/backend/internal/report"
)

func getChatReport(t *testing.T, app core.App, userID, sessionID, reportID string) *httptest.ResponseRecorder {
	t.Helper()
	user, err := app.FindRecordById("users", userID)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: app, Auth: user}
	e.Response = rec
	e.Request = httptest.NewRequest(http.MethodGet, "/api/app/chats/"+sessionID+"/reports/"+reportID, nil)
	e.Request.SetPathValue("id", sessionID)
	e.Request.SetPathValue("report", reportID)
	if err := handleGetChatReport(app)(e); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestChatReportIsLaidOutFromTheStoredConversation(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
	session, err := chat.CreateSession(app, chat.NewSession{UserID: owner, Kind: chat.KindSearch, Mode: chat.ModeResearch, FirstMessage: "Report my spending"})
	if err != nil {
		t.Fatal(err)
	}

	chart := report.Chart{Type: "bar", Title: "Spend", Labels: []string{"2024"}, Series: []report.Series{{Values: []float64{10}}}}
	doc := report.Document{Title: "Ausgaben 2024", Content: "# Spend\n\n![Spend](chart:" + chart.ID() + ")"}
	for _, entry := range []chat.ThreadEntry{
		{Role: chat.RoleUser, Content: "Report my spending"},
		{Role: chat.RoleAssistant, Calls: []ai.ToolCall{
			{ID: "1", Name: "create_chart", Arguments: `{"type":"bar","title":"Spend","labels":["2024"],"series":[{"values":[10]}]}`},
			{ID: "2", Name: "create_pdf", Arguments: `{"title":"Ausgaben 2024","content":"# Spend\n\n![Spend](chart:` + chart.ID() + `)"}`},
		}},
		{Role: chat.RoleTool, CallID: "1", Content: "{}"},
		{Role: chat.RoleTool, CallID: "2", Content: "{}"},
		{Role: chat.RoleAssistant, Content: "[Ausgaben 2024.pdf](pdf:" + doc.ID() + ")"},
	} {
		if _, err := chat.AppendThreadMessage(app, owner, session.Id, entry); err != nil {
			t.Fatal(err)
		}
	}

	rec := getChatReport(t, app, owner, session.Id, doc.ID())
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String()[:min(rec.Body.Len(), 80)])
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") || !strings.Contains(got, "Ausgaben") {
		t.Errorf("content disposition = %q", got)
	}

	if rec := getChatReport(t, app, owner, session.Id, "p0000000000"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown report = %d, want 404", rec.Code)
	}
	if rec := getChatReport(t, app, other, session.Id, doc.ID()); rec.Code != http.StatusNotFound {
		t.Errorf("another account's report = %d, want 404", rec.Code)
	}
}
