package aiprovider

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/openai/openai-go/v3/option"
	"github.com/pocketbase/pocketbase/core"
)

// DocumentHeader names the document a provider request is about. Its value is
// the document's checksum, not the PocketBase record id: the operator's gateway
// gets something it can bill against without learning a row id it could use to
// reach into the archive. A request that is not about exactly one document
// sends no header rather than an invented value.
const DocumentHeader = "x-lemmary-doc-id"

type documentKey struct{}

// WithDocument marks ctx as being about one document, named by its checksum.
// An empty checksum leaves ctx alone: a document whose file has not been hashed
// yet goes out unnamed rather than under a stand-in.
func WithDocument(ctx context.Context, checksum string) context.Context {
	checksum = strings.TrimSpace(checksum)
	if checksum == "" {
		return ctx
	}
	return context.WithValue(ctx, documentKey{}, checksum)
}

// WithDocumentRecord is the one place that knows the header's value is the
// checksum and never the record id.
func WithDocumentRecord(ctx context.Context, document *core.Record) context.Context {
	if document == nil {
		return ctx
	}
	return WithDocument(ctx, document.GetString("checksum"))
}

// DocumentFrom returns the document checksum on ctx, or "" when there is none.
func DocumentFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	checksum, _ := ctx.Value(documentKey{}).(string)
	return checksum
}

// managed is AI_MANAGED, read once at wiring time by config.NewRuntime. It
// lives here rather than being threaded through every constructor because the
// two places that need it are the deepest ones: the Anthropic client the
// opencode SDK builds, and the hand-built OCR requests in internal/ocr.
var managed atomic.Bool

// SetManaged records whether this deployment is the managed one. Called once,
// from config.NewRuntime.
func SetManaged(v bool) { managed.Store(v) }

// Managed reports whether AI_MANAGED is on. Self-hosted installs never send
// DocumentHeader, whatever is on the context.
func Managed() bool { return managed.Load() }

// DocumentOptions is the SDK option that stamps DocumentHeader, and nothing at
// all when this is not the managed deployment. Asked at client construction, so
// a self-hosted install has no middleware to run per request.
// all when this is not the managed deployment. Asked at client construction, so
func DocumentOptions() []option.RequestOption {
	if !Managed() {
		return nil
	}
	return []option.RequestOption{option.WithMiddleware(documentMiddleware)}
}

// documentMiddleware runs per attempt on a request clone, so retries and the
// /responses fallback are stamped too.
func documentMiddleware(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	StampDocument(req)
	return next(req)
}

// StampDocument sets DocumentHeader on a request built by hand: the Mistral and
// Docling OCR calls, which speak neither SDK. A no-op unless this is the
// managed deployment and the context names a document.
func StampDocument(req *http.Request) {
	if req == nil || !Managed() {
		return
	}
	if checksum := DocumentFrom(req.Context()); checksum != "" {
		req.Header.Set(DocumentHeader, checksum)
	}
}
