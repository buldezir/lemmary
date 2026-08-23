package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
)

const mockOCRText = "Invoice INV-1001 from Acme Plumbing for leak repair on 2024-07-15. Total due 250 EUR."

const mockExtractionJSON = `{
  "title": "Acme Plumbing Invoice INV-1001",
  "purpose": "Payment for plumbing leak repair",
  "document_date": "2024-07-15",
  "document_type": "Invoice",
  "correspondent": "Acme Plumbing",
  "tags": ["plumbing", "invoice"],
  "people_or_organizations": ["Acme Plumbing"],
  "summary": "Invoice from Acme Plumbing for leak repair totaling 250 EUR.",
  "confidence": 0.92
}`

const mockChatReply = "Based on the document, the invoice total is 250 EUR for leak repair."

// mockSplitJSON answers a document-boundary request. The counts are deliberately
// sloppy (the last part stops short) so tests also cover server-side
// normalization of a model answer.
const mockSplitJSON = `{
  "parts": [
    {"from": 1, "to": 1, "title": "Acme Plumbing Invoice"},
    {"from": 2, "to": 2, "title": "Bank statement"}
  ]
}`

const mockSearchFinalReply = "I found an Acme Plumbing invoice about a leak repair from July 2024."

// mockServers holds httptest servers for OCR and OpenAI-compatible APIs.
type mockServers struct {
	OCR    *httptest.Server
	OpenAI *httptest.Server

	openaiCalls atomic.Int64
	mu          sync.Mutex
	lastBodies  []string
}

func startMockServers() *mockServers {
	m := &mockServers{}

	m.OCR = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"id":   "mistral-ocr-latest",
						"name": "mistral-ocr-latest",
						"capabilities": map[string]any{
							"completion_chat": false,
							"ocr":             true,
						},
					},
					{
						"id":   "mistral-small-latest",
						"name": "mistral-small-latest",
						"capabilities": map[string]any{
							"completion_chat":  true,
							"function_calling": true,
							"ocr":              false,
						},
					},
					{
						"id":   "mistral-embed",
						"name": "mistral-embed",
						"capabilities": map[string]any{
							"completion_chat": false,
							"ocr":             false,
						},
					},
				},
			})
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions") {
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(m.openAIResponseFromBody(string(body)))
			return
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/ocr") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pages": []map[string]any{
				{"index": 0, "markdown": mockOCRText},
			},
		})
	}))

	m.OpenAI = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == http.MethodGet && strings.HasSuffix(path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "e2e-mock", "name": "e2e-mock"},
					{"id": "e2e-mock-updated", "name": "e2e-mock-updated"},
				},
			})
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if !strings.HasSuffix(path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}

		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.lastBodies = append(m.lastBodies, string(body))
		m.mu.Unlock()
		m.openaiCalls.Add(1)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m.openAIResponseFromBody(string(body)))
	}))

	return m
}

func (m *mockServers) Close() {
	if m.OCR != nil {
		m.OCR.Close()
	}
	if m.OpenAI != nil {
		m.OpenAI.Close()
	}
}

func (m *mockServers) LastOpenAIBodies() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.lastBodies))
	copy(out, m.lastBodies)
	return out
}

func (m *mockServers) ResetOpenAIBodies() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastBodies = nil
}

func (m *mockServers) openAIResponseFromBody(body string) map[string]any {
	var req map[string]any
	_ = json.Unmarshal([]byte(body), &req)

	if strings.Contains(body, `"image_url"`) || strings.Contains(body, `"file_data"`) || strings.Contains(body, `"type":"file"`) {
		return chatCompletion(mockOCRText, nil)
	}

	// Split detection also asks for a JSON object, so it has to be recognized
	// before the extraction branch claims every json_object request.
	if strings.Contains(body, "--- PAGE 1 ---") {
		return chatCompletion(mockSplitJSON, nil)
	}

	// Metadata extraction requests JSON object response format.
	if strings.Contains(body, `"json_object"`) || strings.Contains(body, `"type":"json_object"`) {
		return chatCompletion(mockExtractionJSON, nil)
	}
	if rf, ok := req["response_format"].(map[string]any); ok {
		if rf["type"] == "json_object" {
			return chatCompletion(mockExtractionJSON, nil)
		}
	}

	tools := asSlice(req["tools"])
	messages := asSlice(req["messages"])
	hasSearchTool := strings.Contains(body, "search_documents")
	toolChoice := req["tool_choice"]
	searchAgent := len(tools) > 0 || hasSearchTool || looksLikeSearchAgent(messages)

	// Search agent: if tools are available and no tool results yet, emit a tool call.
	if searchAgent && !toolChoiceIsNone(toolChoice) && !messagesHaveToolResults(messages) && !strings.Contains(body, `"role":"tool"`) {
		return chatCompletion("", []map[string]any{{
			"id":   "call_search_1",
			"type": "function",
			"function": map[string]any{
				"name":      "search_documents",
				"arguments": `{"query":"Acme Plumbing","limit":10}`,
			},
		}})
	}

	if searchAgent {
		return chatCompletion(mockSearchFinalReply, nil)
	}
	return chatCompletion(mockChatReply, nil)
}

func asSlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	default:
		return nil
	}
}

func chatCompletion(content string, toolCalls []map[string]any) map[string]any {
	msg := map[string]any{
		"role":    "assistant",
		"content": content,
	}
	finish := "stop"
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
		msg["content"] = nil
		finish = "tool_calls"
	}
	return map[string]any{
		"id":      "chatcmpl-e2e",
		"object":  "chat.completion",
		"created": 1,
		"model":   "e2e-mock",
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": finish,
		}},
	}
}

func toolChoiceIsNone(toolChoice any) bool {
	switch v := toolChoice.(type) {
	case string:
		return v == "none"
	case map[string]any:
		if t, ok := v["type"].(string); ok {
			return t == "none"
		}
	}
	return false
}

func messagesHaveToolResults(messages []any) bool {
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "tool" {
			return true
		}
		// DSML / user-fed tool results often arrive as user messages after an assistant tool call.
		if role == "user" {
			content, _ := msg["content"].(string)
			if strings.Contains(content, "tool") && strings.Contains(content, "result") {
				return true
			}
		}
	}
	return false
}

func looksLikeSearchAgent(messages []any) bool {
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		if role != "system" {
			continue
		}
		lower := strings.ToLower(content)
		if strings.Contains(lower, "search_documents") ||
			strings.Contains(lower, "deep search") ||
			strings.Contains(lower, "keyword") {
			return true
		}
	}
	return false
}
