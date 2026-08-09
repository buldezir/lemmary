package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPasswordResetStoredInOutboundEmails(t *testing.T) {
	h := StartShared(t)

	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/users/request-password-reset", "", map[string]string{
		"email": UserEmail,
	})
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("request-password-reset: status %d body %s", status, raw)
	}

	deadline := time.Now().Add(5 * time.Second)
	var records []map[string]any
	for {
		mails, err := h.App.FindRecordsByFilter(
			"outbound_emails",
			"subject ~ {:subj}",
			"-created",
			10,
			0,
			map[string]any{"subj": "password"},
		)
		if err != nil {
			t.Fatalf("find outbound_emails: %v", err)
		}
		if len(mails) > 0 {
			records = make([]map[string]any, 0, len(mails))
			for _, r := range mails {
				records = append(records, map[string]any{
					"from_address": r.GetString("from_address"),
					"subject":      r.GetString("subject"),
					"html":         r.GetString("html"),
					"to":           r.Get("to"),
				})
				break
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for outbound_emails row")
		}
		time.Sleep(50 * time.Millisecond)
	}

	subject, _ := records[0]["subject"].(string)
	if !strings.Contains(strings.ToLower(subject), "password") {
		t.Fatalf("unexpected subject %q", subject)
	}
	from, _ := records[0]["from_address"].(string)
	if from == "" {
		t.Fatal("expected from_address")
	}
	html, _ := records[0]["html"].(string)
	if html == "" && records[0]["to"] == nil {
		t.Fatal("expected html or to recipients")
	}

	// Superuser can list via collection API; regular users cannot.
	super := h.superToken(t)
	status, raw = h.doJSON(t, http.MethodGet, "/api/collections/outbound_emails/records?perPage=1", super, nil)
	if status != http.StatusOK {
		t.Fatalf("superuser list outbound_emails: %d %s", status, raw)
	}
	var list struct {
		TotalItems int `json:"totalItems"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("decode list: %v body %s", err, raw)
	}
	if list.TotalItems < 1 {
		t.Fatalf("expected at least 1 outbound_emails item, got %d", list.TotalItems)
	}

	user := h.userToken(t)
	status, raw = h.doJSON(t, http.MethodGet, "/api/collections/outbound_emails/records?perPage=1", user, nil)
	if status == http.StatusOK {
		t.Fatalf("regular user should not list outbound_emails, got %s", raw)
	}
}
