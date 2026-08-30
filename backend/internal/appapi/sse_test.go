package appapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
)

func TestSSEWriterFramesEvents(t *testing.T) {
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Response = rec
	e.Request = httptest.NewRequest("POST", "/api/app/search/stream", nil)

	stream := newSSEWriter(e)
	stream.Send(ai.ResearchEvent{Type: "step", Kind: "search", Status: "start", Query: "car"})
	stream.Send(ai.ResearchEvent{Type: "done"})

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("proxies may buffer the stream: X-Accel-Buffering = %q", got)
	}

	body := rec.Body.String()
	frames := strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n")
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2: %q", len(frames), body)
	}

	var first ai.ResearchEvent
	payload, ok := strings.CutPrefix(frames[0], "data: ")
	if !ok {
		t.Fatalf("frame is not an SSE data line: %q", frames[0])
	}
	if err := json.Unmarshal([]byte(payload), &first); err != nil {
		t.Fatalf("decode frame: %v (%q)", err, payload)
	}
	if first.Type != "step" || first.Kind != "search" || first.Query != "car" {
		t.Fatalf("first event = %+v", first)
	}
	if !strings.HasSuffix(strings.TrimSpace(frames[1]), `{"type":"done"}`) {
		t.Fatalf("last frame should close the stream, got %q", frames[1])
	}
}

func TestIsResearchModeIgnoresLegacyValues(t *testing.T) {
	t.Parallel()
	if !isResearchMode("research") || !isResearchMode("  Research  ") {
		t.Fatal("research mode should be recognised regardless of case and padding")
	}
	// "deep" is gone; an old client sending it gets plain search, not an error.
	for _, legacy := range []string{"", "shallow", "deep", "nonsense"} {
		if isResearchMode(legacy) {
			t.Fatalf("%q should fall back to search mode", legacy)
		}
	}
}
