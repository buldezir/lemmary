package ai

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// helperHarness fakes the helper's endpoint: canned replies in order, and a
// record of what was asked.
type helperHarness struct {
	mu       sync.Mutex
	replies  []helperReply
	next     int
	requests []map[string]any
}

type helperReply struct {
	content    string
	httpStatus int
	message    string
}

func (h *helperHarness) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	h.mu.Lock()
	h.requests = append(h.requests, body)
	reply := helperReply{content: `{"documents":[]}`}
	if h.next < len(h.replies) {
		reply = h.replies[h.next]
		h.next++
	}
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if reply.httpStatus != 0 {
		w.WriteHeader(reply.httpStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": reply.message, "type": "invalid_request_error"},
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-helper",
		"object":  "chat.completion",
		"created": 1,
		"model":   "helper",
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": reply.content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 30},
	})
}

func newHelper(t *testing.T, replies ...helperReply) (*helperHarness, Helper) {
	t.Helper()
	h := &helperHarness{replies: replies}
	srv := httptest.NewServer(http.HandlerFunc(h.handler))
	t.Cleanup(srv.Close)
	return h, NewHelper("openai", "test-key", "cheap-model", srv.URL, 5*time.Second, slog.Default())
}

func TestHelperDistillParsesLenientlyAndKeepsOnlyAskedDocuments(t *testing.T) {
	t.Parallel()
	_, helper := newHelper(t, helperReply{content: "```json\n" + `{"documents":[
		{"id":"a","relevant":"true","notes":"Rent is 900 EUR.","quotes":["Kaltmiete 900 EUR"],"values":{"amount":900,"amount_currency":"eur","bogus":"x"},"missing":[]},
		{"id":"b","relevant":false},
		{"id":"stranger","relevant":true,"notes":"not asked"}
	]}` + "\n```"})

	result, err := helper.Distill(context.Background(), DistillRequest{
		Question: "How much is the rent?",
		Fields:   []SurveyField{{Name: "amount", Type: "number"}},
		Docs: []DistillDoc{
			{ID: "a", Title: "Lease", Text: "Kaltmiete 900 EUR monatlich."},
			{ID: "b", Title: "Receipt", Text: "Paid for groceries."},
		},
	})
	if err != nil {
		t.Fatalf("Distill: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("rows = %+v, want the two asked documents only", result.Rows)
	}
	a := result.Rows[0]
	if a.ID != "a" || !a.Relevant || a.Notes != "Rent is 900 EUR." || len(a.Quotes) != 1 {
		t.Fatalf("row a = %+v", a)
	}
	if a.Values["amount"] != "900" || a.Values["amount_currency"] != "eur" {
		t.Fatalf("values = %v; a numeric value should be kept as a string, and the currency companion with it", a.Values)
	}
	if _, ok := a.Values["bogus"]; ok {
		t.Fatalf("a field that was not asked for was kept: %v", a.Values)
	}
	if result.Rows[1].ID != "b" || result.Rows[1].Relevant {
		t.Fatalf("row b = %+v", result.Rows[1])
	}
	if result.Usage.Prompt != 120 || result.Usage.Completion != 30 {
		t.Fatalf("usage = %+v, want the provider's numbers", result.Usage)
	}
}

func TestHelperDistillRetriesWithoutJSONModeWhenRejected(t *testing.T) {
	t.Parallel()
	h, helper := newHelper(t,
		helperReply{httpStatus: http.StatusBadRequest, message: "response_format is not supported by this model"},
		helperReply{content: `Here you go: {"documents":[{"id":"a","relevant":true,"notes":"ok"}]}`},
	)

	result, err := helper.Distill(context.Background(), DistillRequest{
		Question: "anything?",
		Docs:     []DistillDoc{{ID: "a", Text: "text"}},
	})
	if err != nil {
		t.Fatalf("Distill: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0].Notes != "ok" {
		t.Fatalf("rows = %+v", result.Rows)
	}
	if len(h.requests) != 2 {
		t.Fatalf("requests = %d, want the rejected one and the retry", len(h.requests))
	}
	if _, ok := h.requests[0]["response_format"]; !ok {
		t.Fatal("first request should have asked for JSON mode")
	}
	if _, ok := h.requests[1]["response_format"]; ok {
		t.Fatal("retry should have dropped response_format")
	}
}

func TestHelperDistillPromptCarriesEveryDocumentAndTheFields(t *testing.T) {
	t.Parallel()
	h, helper := newHelper(t)
	_, err := helper.Distill(context.Background(), DistillRequest{
		Question: "What was invoiced?",
		Fields:   []SurveyField{{Name: "total", Type: "number", Description: "invoice total"}},
		Docs: []DistillDoc{
			{ID: "one", Title: "Invoice 1", Text: "Total 10 EUR"},
			{ID: "two", Title: "Invoice 2", Text: "Total 20 EUR", Excerpted: true},
		},
	})
	if err != nil {
		t.Fatalf("Distill: %v", err)
	}
	messages, _ := h.requests[0]["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want system + user", len(messages))
	}
	system := messages[0].(map[string]any)["content"].(string)
	user := messages[1].(map[string]any)["content"].(string)
	for _, want := range []string{"=== document one ===", "=== document two ===", "Total 10 EUR", "Total 20 EUR", "Excerpt:", "What was invoiced?"} {
		if !strings.Contains(user, want) {
			t.Fatalf("user message missing %q:\n%s", want, user)
		}
	}
	for _, want := range []string{"total (number): invoice total", "_currency"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, system)
		}
	}
	if h.requests[0]["temperature"] != float64(0) {
		t.Fatalf("temperature = %v, want 0 for extraction", h.requests[0]["temperature"])
	}
}

func TestHelperDistillRejectsAnEmptyQuestion(t *testing.T) {
	t.Parallel()
	_, helper := newHelper(t)
	if _, err := helper.Distill(context.Background(), DistillRequest{Docs: []DistillDoc{{ID: "a", Text: "x"}}}); err == nil {
		t.Fatal("a distil without a question should be refused")
	}
}

// The helper inherits the general AI timeout, which is sized for the main
// model. A helper call is the cheap, disposable leg of a run -- a batch that
// fails is passed through as raw text -- so waiting minutes on one buys
// nothing and lengthens the run for every batch that hangs.
func TestHelperTimeoutIsCapped(t *testing.T) {
	for _, tc := range []struct {
		name string
		give time.Duration
		want time.Duration
	}{
		{"a generous shared timeout is capped", 10 * time.Minute, maxHelperTimeout},
		{"unset falls back to the cap", 0, maxHelperTimeout},
		{"a shorter timeout is respected", 10 * time.Second, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := helperTimeout(tc.give); got != tc.want {
				t.Fatalf("timeout = %s, want %s", got, tc.want)
			}
		})
	}
}
