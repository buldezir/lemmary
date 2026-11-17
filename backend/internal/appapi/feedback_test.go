package appapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/testpb"
)

func postFeedback(t *testing.T, app core.App, rt *config.Runtime, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: app}
	e.Response = rec
	e.Request = httptest.NewRequest(http.MethodPost, "/api/app/feedback", strings.NewReader(body))
	e.Request.Header.Set("Content-Type", "application/json")
	e.Request.Header.Set("User-Agent", "test-agent")
	e.Request.Header.Set("Accept-Language", "de")
	if err := handlePostFeedback(app, rt, target)(e); err != nil {
		t.Fatal(err)
	}
	return rec
}

// Not parallel: NewRuntime writes the process-global managed flag.
func TestPostFeedbackRelaysToThePanel(t *testing.T) {
	prev := aiprovider.Managed()
	t.Cleanup(func() { aiprovider.SetManaged(prev) })
	t.Setenv(config.EnvAIAPIKey, "lpx_tenant")

	var got []map[string]string
	var auth []string
	status := http.StatusCreated
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = append(got, b)
		auth = append(auth, r.Header.Get("Authorization"))
		w.WriteHeader(status)
	}))
	t.Cleanup(upstream.Close)

	app := testpb.Open(t)
	selfHosted := config.NewRuntime(config.AIEnv{})
	body := `{"kind":"bug","message":"  broken  ","email":"a@b.c","page":"/documents"}`
	if rec := postFeedback(t, app, selfHosted, upstream.URL, body); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	b := got[0]
	if b["kind"] != "bug" || b["message"] != "broken" || b["email"] != "a@b.c" || b["page"] != "/documents" ||
		b["locale"] != "de" || b["user_agent"] != "test-agent" || b["app_version"] != "dev" || b["logs"] != "" {
		t.Fatalf("relayed %v", b)
	}
	if !feedbackInstallID.MatchString(b["install_id"]) {
		t.Fatalf("install_id = %q", b["install_id"])
	}
	if auth[0] != "" {
		t.Fatalf("self-hosted sent Authorization %q", auth[0])
	}

	managed := config.NewRuntime(config.AIEnv{Managed: true})
	postFeedback(t, app, managed, upstream.URL, body)
	if got[1]["install_id"] != b["install_id"] {
		t.Fatalf("install id changed: %q then %q", b["install_id"], got[1]["install_id"])
	}
	if auth[1] != "Bearer lpx_tenant" {
		t.Fatalf("managed sent Authorization %q", auth[1])
	}

	status = http.StatusTooManyRequests
	if rec := postFeedback(t, app, selfHosted, upstream.URL, body); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited status = %d", rec.Code)
	}
	status = http.StatusInternalServerError
	if rec := postFeedback(t, app, selfHosted, upstream.URL, body); rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream failure status = %d", rec.Code)
	}
}

func TestPostFeedbackRefusesBeforeSending(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("nothing should be sent")
	}))
	t.Cleanup(upstream.Close)
	app := testpb.Open(t)
	rt := config.NewRuntime(config.AIEnv{})

	for body, want := range map[string]int{
		`{"kind":"rant","message":"x"}`:                                                http.StatusBadRequest,
		`{"kind":"idea","message":"   "}`:                                              http.StatusBadRequest,
		`{"kind":"idea","message":"` + strings.Repeat("x", 5001) + `"}`:                http.StatusBadRequest,
		`{"kind":"bug","message":"x","log_minutes":4}`:                                 http.StatusBadRequest,
		`{"kind":"bug","message":"x","email":"` + strings.Repeat("a", 250) + `@b.cd"}`: http.StatusBadRequest,
		`{"kind":"bug","message":"x","log_minutes":1}`:                                 http.StatusForbidden,
	} {
		if rec := postFeedback(t, app, rt, upstream.URL, body); rec.Code != want {
			t.Errorf("%.40s: status = %d, want %d", body, rec.Code, want)
		}
	}
}

func saveLog(t *testing.T, app core.App, msg string, ago time.Duration) {
	t.Helper()
	saveLogData(t, app, msg, map[string]any{}, ago)
}

func saveLogData(t *testing.T, app core.App, msg string, data map[string]any, ago time.Duration) {
	t.Helper()
	l := &core.Log{Message: msg, Data: data, Created: types.NowDateTime().Add(-ago)}
	l.Id = core.GenerateDefaultRandomId()
	if err := app.AuxSave(l); err != nil {
		t.Fatal(err)
	}
}

func TestFeedbackLogTailKeepsTheWindowOldestFirst(t *testing.T) {
	app := testpb.Open(t)
	saveLog(t, app, "too old", 3*time.Minute)
	saveLog(t, app, "first", 90*time.Second)
	saveLog(t, app, "second", time.Second)

	tail := feedbackLogTail(app, 2*time.Minute)
	if strings.Contains(tail, "too old") {
		t.Fatalf("tail reaches past the window: %q", tail)
	}
	if i, j := strings.Index(tail, "first"), strings.Index(tail, "second"); i < 0 || j < i {
		t.Fatalf("tail = %q", tail)
	}
}

func TestFeedbackLogTailRedactsFileTokens(t *testing.T) {
	app := testpb.Open(t)
	saveLogData(t, app, "GET /api/files/c/r/a.pdf?thumb=1&token=secret1", map[string]any{
		"url":     "/api/files/c/r/a.pdf?thumb=1&token=secret2",
		"referer": "https://x/api/files/c/r/b.pdf?token=secret3",
	}, time.Second)

	tail := feedbackLogTail(app, time.Minute)
	if strings.Contains(tail, "secret") || strings.Count(tail, "token=REDACTED") != 3 {
		t.Fatalf("tail = %q", tail)
	}
	if !strings.Contains(tail, "thumb=1") {
		t.Fatalf("other query values should stay: %q", tail)
	}
}

func TestFeedbackLogTailFitsThePanelAndKeepsTheNewest(t *testing.T) {
	app := testpb.Open(t)
	big := strings.Repeat("x", 20<<10)
	for i := range 80 {
		saveLog(t, app, fmt.Sprintf("line-%02d %s", i, big), time.Duration(80-i)*time.Second)
	}

	tail := feedbackLogTail(app, 5*time.Minute)
	if len(tail) > feedbackMaxLogs {
		t.Fatalf("tail is %d bytes, over %d", len(tail), feedbackMaxLogs)
	}
	if !strings.Contains(tail, "line-79") || strings.Contains(tail, "line-00") {
		t.Fatal("tail should keep the newest lines and drop the oldest")
	}
}
