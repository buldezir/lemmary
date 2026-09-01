package appapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

// TestSSEWriterHeartbeatKeepsTheConnectionBusy covers the gap this stream has
// by design: nothing is sent for the whole of each model completion, and the
// first one happens before any step event. A reverse proxy with a 30-60s idle
// timeout drops that connection while the server is still working.
func TestSSEWriterHeartbeatKeepsTheConnectionBusy(t *testing.T) {
	rec := &syncRecorder{header: http.Header{}}
	e := &core.RequestEvent{}
	e.Response = rec
	e.Request = httptest.NewRequest("POST", "/api/app/search/stream", nil)

	stream := newSSEWriter(e)
	stop := stream.heartbeatEvery(context.Background(), time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(rec.String(), ": ping") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stop()

	body := rec.String()
	if !strings.Contains(body, ": ping\n\n") {
		t.Fatalf("no heartbeat frame was written: %q", body)
	}
	// A comment frame carries no data line, so a client parsing "data: " sees
	// nothing at all -- which is the point.
	if strings.Contains(body, "data: ") {
		t.Fatalf("heartbeat should not look like an event: %q", body)
	}

	// Nothing may be written after stop returns: the handler is about to
	// return, and the server will recycle the response writer.
	after := len(rec.String())
	time.Sleep(20 * time.Millisecond)
	if got := len(rec.String()); got != after {
		t.Fatalf("heartbeat kept writing after stop: %d -> %d bytes", after, got)
	}
}

// syncRecorder is httptest.NewRecorder with a lock, so a test can read the body
// while the heartbeat goroutine is still writing to it. The writer under test
// serializes its own writes; the recorder is what cannot be read concurrently.
type syncRecorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
	status int
}

func (r *syncRecorder) Header() http.Header { return r.header }

func (r *syncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *syncRecorder) WriteHeader(status int) { r.status = status }

func (r *syncRecorder) Flush() {}

func (r *syncRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}
