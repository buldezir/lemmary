package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/mail"
)

type fakeGmail struct {
	ids  []string
	msgs map[string]*mail.Message
	atts map[string][]byte
}

func (f *fakeGmail) ListMessageIDs(ctx context.Context, query string, max int) ([]string, error) {
	return append([]string{}, f.ids...), nil
}

func (f *fakeGmail) GetMessage(ctx context.Context, id string, mode string) (*mail.Message, error) {
	msg := f.msgs[id]
	cp := *msg
	if mode != mail.ModeDeep {
		cp.BodyText = ""
	}
	return &cp, nil
}

func (f *fakeGmail) DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	return f.atts[messageID+":"+attachmentID], nil
}

func TestMailStatusAndAccountCRUD(t *testing.T) {
	h := StartShared(t)
	tok := h.userToken(t)

	status, raw := h.doJSON(t, http.MethodGet, "/api/app/mail/status", tok, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	var st map[string]any
	_ = json.Unmarshal([]byte(raw), &st)
	if st["google_oauth_configured"] != true {
		t.Fatalf("expected google oauth configured: %v", st)
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/mail/accounts", tok, map[string]any{
		"email":         UserEmail,
		"refresh_token": "e2e-refresh-token",
	})
	if status != http.StatusOK {
		t.Fatalf("create account %d: %s", status, raw)
	}
	var account mail.AccountDTO
	if err := json.Unmarshal([]byte(raw), &account); err != nil {
		t.Fatal(err)
	}
	if account.Email != UserEmail || !account.Enabled {
		t.Fatalf("%+v", account)
	}

	status, raw = h.doJSON(t, http.MethodPatch, "/api/app/mail/accounts/"+account.ID, tok, map[string]any{
		"cron_enabled":       true,
		"cron_lookback_days": 14,
		"triage_mode":        "deep",
	})
	if status != http.StatusOK {
		t.Fatalf("patch %d: %s", status, raw)
	}
	_ = json.Unmarshal([]byte(raw), &account)
	if !account.CronEnabled || account.CronLookbackDays != 14 || account.TriageMode != "deep" {
		t.Fatalf("%+v", account)
	}

	status, raw = h.doJSON(t, http.MethodGet, "/api/app/mail/accounts", tok, nil)
	if status != http.StatusOK {
		t.Fatalf("list %d: %s", status, raw)
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/mail/accounts", tok, map[string]any{
		"email":         "other@example.com",
		"refresh_token": "x",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for email mismatch, got %d: %s", status, raw)
	}

	status, _ = h.doJSON(t, http.MethodDelete, "/api/app/mail/accounts/"+account.ID, tok, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete %d", status)
	}
}

func TestMailSuperuserCanLinkByEmail(t *testing.T) {
	h := StartShared(t)
	superTok := h.superToken(t)

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/mail/accounts", superTok, map[string]any{
		"email":         UserEmail,
		"refresh_token": "e2e-super-link-token",
	})
	if status != http.StatusOK {
		t.Fatalf("superuser create account %d: %s", status, raw)
	}
	var account mail.AccountDTO
	if err := json.Unmarshal([]byte(raw), &account); err != nil {
		t.Fatal(err)
	}
	if account.Email != UserEmail {
		t.Fatalf("%+v", account)
	}

	// Superuser should see the account in the list.
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/mail/accounts", superTok, nil)
	if status != http.StatusOK {
		t.Fatalf("list %d: %s", status, raw)
	}
	if !strings.Contains(string(raw), account.ID) {
		t.Fatalf("expected account in list: %s", raw)
	}

	status, _ = h.doJSON(t, http.MethodDelete, "/api/app/mail/accounts/"+account.ID, superTok, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete %d", status)
	}
}

func TestMailScanImportsAttachment(t *testing.T) {
	h := StartShared(t)
	tok := h.userToken(t)

	fake := &fakeGmail{
		ids: []string{"msg1"},
		msgs: map[string]*mail.Message{
			"msg1": {
				ID:      "msg1",
				Subject: "Your invoice",
				From:    "billing@acme.test",
				Date:    "Mon, 15 Jul 2024 10:00:00 +0000",
				Attachments: []mail.AttachmentMeta{
					{ID: "att1", Filename: "invoice.pdf", MimeType: "application/pdf"},
					{ID: "att2", Filename: "logo.png", MimeType: "image/png"},
				},
			},
		},
		atts: map[string][]byte{
			"msg1:att1": []byte("%PDF-1.4 e2e invoice"),
			"msg1:att2": []byte("png-bytes"),
		},
	}
	mail.SetClientFactory(func(app core.App, refreshToken string) (mail.Client, error) {
		return fake, nil
	})
	t.Cleanup(func() { mail.SetClientFactory(nil) })

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/mail/accounts", tok, map[string]any{
		"email":         UserEmail,
		"refresh_token": "e2e-scan-token",
	})
	if status != http.StatusOK {
		t.Fatalf("create account %d: %s", status, raw)
	}
	var account mail.AccountDTO
	_ = json.Unmarshal([]byte(raw), &account)
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/app/mail/accounts/"+account.ID, tok, nil)
	})

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/mail/accounts/"+account.ID+"/scans", tok, map[string]any{
		"date_from": "2024-07-01",
		"date_to":   "2024-07-31",
		"mode":      "simple",
	})
	if status != http.StatusAccepted {
		t.Fatalf("create scan %d: %s", status, raw)
	}
	var scan mail.ScanDTO
	_ = json.Unmarshal([]byte(raw), &scan)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status, raw = h.doJSON(t, http.MethodGet, "/api/app/mail/scans/"+scan.ID, tok, nil)
		if status != http.StatusOK {
			t.Fatalf("get scan %d: %s", status, raw)
		}
		_ = json.Unmarshal([]byte(raw), &scan)
		if scan.Status == mail.ScanCompleted || scan.Status == mail.ScanFailed {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if scan.Status != mail.ScanCompleted {
		t.Fatalf("scan not completed: %+v", scan)
	}
	if scan.Progress.Imported < 1 {
		t.Fatalf("expected import, progress=%+v", scan.Progress)
	}

	docs, err := h.App.FindRecordsByFilter("documents", "user = {:user}", "-created", 10, 0, map[string]any{"user": h.UserID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range docs {
		if d.GetString("title") == "invoice" || d.GetString("file") != "" {
			// imported file name becomes title without extension
			if d.GetString("title") == "invoice" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected imported document titled invoice, got %d docs", len(docs))
	}
}
