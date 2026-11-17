package appapi

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/strutil"
	"lemmary/backend/internal/updatecheck"
)

const (
	feedbackMaxMessage = 5000
	// The panel stores up to 512 KB of logs; the margin leaves room for the
	// line that would have crossed it.
	feedbackMaxLogs = 500 << 10
	// Bounds the read, not the window: lines past feedbackMaxLogs are dropped
	// anyway.
	feedbackLogRows = 5000
)

var (
	feedbackClient     = &http.Client{Timeout: 20 * time.Second}
	feedbackKinds      = []string{"bug", "idea", "other"}
	feedbackLogMinutes = []int{0, 1, 2, 3, 5}
	feedbackInstallID  = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
)

type feedbackRequest struct {
	Kind       string `json:"kind"`
	Message    string `json:"message"`
	Email      string `json:"email"`
	Page       string `json:"page"`
	LogMinutes int    `json:"log_minutes"`
}

// handlePostFeedback relays a report to the Lemmary maintainers' panel. The
// browser cannot post there itself: the panel sends no CORS headers, and the
// install id, version, logs and a managed tenant's token are the server's.
func handlePostFeedback(app core.App, rt *config.Runtime, target string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req feedbackRequest
		if err := e.BindBody(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		req.Message = strings.TrimSpace(req.Message)
		if !slices.Contains(feedbackKinds, req.Kind) {
			return writeError(e, http.StatusBadRequest, "Choose what kind of feedback this is.")
		}
		if req.Message == "" {
			return writeError(e, http.StatusBadRequest, "A message is required.")
		}
		if len([]rune(req.Message)) > feedbackMaxMessage {
			return writeBadRequest(e, i18n.Errorf("Keep the message under %d characters.", feedbackMaxMessage))
		}
		if !slices.Contains(feedbackLogMinutes, req.LogMinutes) {
			return writeError(e, http.StatusBadRequest, "Choose how many minutes of logs to attach.")
		}
		if req.LogMinutes > 0 && !IsAppAdmin(e) {
			return writeError(e, http.StatusForbidden, "Admin access required.")
		}

		installID, err := feedbackInstallIDOf(app)
		if err != nil {
			app.Logger().Error("feedback install id unavailable", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Feedback could not be sent. Try again later.")
		}
		body := map[string]string{
			"kind":        req.Kind,
			"message":     req.Message,
			"email":       strings.TrimSpace(req.Email),
			"install_id":  installID,
			"app_version": strutil.FirstNonEmpty(os.Getenv(updatecheck.EnvVersion), "dev"),
			"locale":      i18n.FromRequest(e.Request),
			"page":        strutil.TruncateRunes(req.Page, 500),
			"user_agent":  strutil.TruncateRunes(e.Request.UserAgent(), 500),
		}
		if req.LogMinutes > 0 {
			body["logs"] = feedbackLogTail(app, time.Duration(req.LogMinutes)*time.Minute)
		}
		status, err := sendFeedback(e.Request, target, feedbackToken(rt), body)
		return writeFeedbackResult(e, app, status, err)
	}
}

func writeFeedbackResult(e *core.RequestEvent, app core.App, status int, err error) error {
	switch {
	case err != nil:
		app.Logger().Warn("feedback not delivered", slog.Any("error", err))
	case status == http.StatusCreated:
		return e.NoContent(http.StatusNoContent)
	case status == http.StatusTooManyRequests:
		return writeError(e, http.StatusTooManyRequests, "Too much feedback from this install. Try again in an hour.")
	case status == http.StatusBadRequest:
		return writeError(e, http.StatusBadRequest, "Check the reply email address.")
	default:
		app.Logger().Warn("feedback refused", slog.Int("status", status))
	}
	return writeError(e, http.StatusBadGateway, "Feedback could not be sent. Try again later.")
}

func sendFeedback(r *http.Request, target, token string, body map[string]string) (int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := feedbackClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// feedbackToken is a managed tenant's panel token, which tells the panel which
// workspace wrote. A self-hosted install's key belongs to some AI provider and
// never leaves for the panel.
func feedbackToken(rt *config.Runtime) string {
	key := strings.TrimSpace(os.Getenv(config.EnvAIAPIKey))
	if rt.Managed() && strings.HasPrefix(key, "lpx_") {
		return key
	}
	return ""
}

// feedbackInstallIDOf is a random id kept in the data directory, so it travels
// with a backup and says nothing about the install it names.
func feedbackInstallIDOf(app core.App) (string, error) {
	path := filepath.Join(app.DataDir(), "install_id")
	if raw, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(raw)); feedbackInstallID.MatchString(id) {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	id := rand.Text()
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// feedbackLogTail is the PocketBase log lines of the last window, oldest first.
// When they do not all fit the panel's limit, the newest are kept.
func feedbackLogTail(app core.App, window time.Duration) string {
	since := types.NowDateTime().Add(-window).String()
	var logs []*core.Log
	err := app.LogQuery().
		AndWhere(dbx.NewExp("created >= {:since}", dbx.Params{"since": since})).
		OrderBy("created DESC").
		Limit(feedbackLogRows).
		All(&logs)
	if err != nil {
		return "logs unavailable: " + err.Error()
	}
	lines := make([]string, 0, len(logs))
	size := 0
	for _, l := range logs {
		data, _ := json.Marshal(l.Data)
		// One huge entry must not crowd out every other line.
		line := strutil.Truncate(fmt.Sprintf("%s %s %s %s", l.Created.String(), slog.Level(l.Level), l.Message, data), 8<<10)
		if size += len(line) + 1; size > feedbackMaxLogs {
			break
		}
		lines = append(lines, line)
	}
	slices.Reverse(lines)
	return strings.Join(lines, "\n")
}
