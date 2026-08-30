package appapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

// sseWriter streams server-sent events over a request that is otherwise an
// ordinary POST — the message history is the request body, so the browser reads
// this with fetch() rather than EventSource.
type sseWriter struct {
	e *core.RequestEvent
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
	return &sseWriter{e: e}
}

// Send writes one event. A write failure means the client is gone; the caller
// finds that out through the request context rather than through an error here.
func (w *sseWriter) Send(payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(w.e.Response, "data: %s\n\n", encoded); err != nil {
		return
	}
	_ = w.e.Flush()
}
