package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// heartbeatInterval is well under the 30-60s idle timeout a reverse proxy
// applies, which otherwise drops the connection during a model completion.
const heartbeatInterval = 15 * time.Second

// sseWriter streams events over an ordinary POST: the message history is the
// request body, so the browser reads this with fetch() rather than EventSource.
type sseWriter struct {
	e *core.RequestEvent
	// Watched only to stop writing, never to stop the run: a write to a
	// half-closed socket can block for as long as the kernel's send buffer
	// stays full, stalling a goroutine that still has a turn to store.
	ctx context.Context
	// Guards the response writer: the heartbeat runs on its own goroutine, and
	// a ResponseWriter may not be written by two at once.
	mu sync.Mutex
}

func newSSEWriter(e *core.RequestEvent) *sseWriter {
	header := e.Response.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	// Tell an intermediate proxy not to buffer, or the steps all arrive at
	// the end.
	header.Set("X-Accel-Buffering", "no")
	e.Response.WriteHeader(http.StatusOK)
	_ = e.Flush()
	return &sseWriter{e: e, ctx: e.Request.Context()}
}

func (w *sseWriter) gone() bool { return w.ctx != nil && w.ctx.Err() != nil }

// Send reports no error: a write failure means the client is gone, which the
// caller learns from the request context instead.
func (w *sseWriter) Send(payload any) {
	if w.gone() {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.gone() {
		return
	}
	if _, err := fmt.Fprintf(w.e.Response, "data: %s\n\n", encoded); err != nil {
		return
	}
	_ = w.e.Flush()
}

// ping writes a comment frame every SSE client ignores; it exists only so the
// connection is not idle.
func (w *sseWriter) ping() {
	if w.gone() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.gone() {
		return
	}
	if _, err := fmt.Fprint(w.e.Response, ": ping\n\n"); err != nil {
		return
	}
	_ = w.e.Flush()
}

// Heartbeat's stop must be called before the handler returns: writing to the
// response after that races with the server recycling it.
func (w *sseWriter) Heartbeat(ctx context.Context) (stop func()) {
	return w.heartbeatEvery(ctx, heartbeatInterval)
}

func (w *sseWriter) heartbeatEvery(ctx context.Context, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.ping()
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
