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

// heartbeatInterval is how often an idle stream sends a comment frame. A
// research run is silent for the whole of each model completion, and a long
// one routinely outlasts the 30-60s idle timeout a reverse proxy applies --
// which drops the connection while the server is still working. Well under
// the shortest of those defaults.
const heartbeatInterval = 15 * time.Second

// sseWriter streams server-sent events over a request that is otherwise an
// ordinary POST — the message history is the request body, so the browser reads
// this with fetch() rather than EventSource.
type sseWriter struct {
	e *core.RequestEvent
	// The request's context, watched only to stop writing. A run outlives its
	// connection deliberately, and a write to a half-closed socket can block
	// for as long as the kernel's send buffer stays full -- which would stall
	// the goroutine that still has a turn to store. Once the client is known
	// gone there is nobody to write for, so every frame is dropped instead.
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
	// Tell an intermediate proxy not to buffer, or the steps arrive all at once
	// at the end and the whole point is lost.
	header.Set("X-Accel-Buffering", "no")
	e.Response.WriteHeader(http.StatusOK)
	_ = e.Flush()
	return &sseWriter{e: e, ctx: e.Request.Context()}
}

// gone reports that the client has hung up, so there is nothing to write to.
func (w *sseWriter) gone() bool { return w.ctx != nil && w.ctx.Err() != nil }

// Send writes one event. A write failure means the client is gone; the caller
// finds that out through the request context rather than through an error here.
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

// ping writes a comment frame. Every SSE client ignores it -- it exists so the
// connection is not idle -- so it carries no payload and no event type.
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

// Heartbeat keeps the connection from going idle for the life of the returned
// stop function, which must be called before the handler returns: writing to
// the response after that races with the server recycling it.
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
