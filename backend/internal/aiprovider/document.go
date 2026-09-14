package aiprovider

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/openai/openai-go/option"
	"github.com/pocketbase/pocketbase/core"
)

// DocumentHeader names the document a provider request is about. Its value is
// the document's checksum -- the SHA-256 of the stored file -- not the
// PocketBase record id: the operator's gateway gets something it can bill
// against without learning a row id it could use to reach into the archive.
// The managed gateway bills and attributes per document; a request that is not
// about exactly one -- model discovery, an OCR test upload, an archive-wide
// search, a query embedding -- sends no header rather than an invented value.
const DocumentHeader = "x-lemmary-doc-id"

type documentKey struct{}

// WithDocument marks ctx as being about one document, named by its checksum.
// Every provider request made under it carries that checksum, in managed mode.
// An empty checksum leaves ctx alone: a document whose file has not been hashed
// yet -- or a duplicate, which gives its checksum up to the original -- goes out
// unnamed rather than under a stand-in.
func WithDocument(ctx context.Context, checksum string) context.Context {
	checksum = strings.TrimSpace(checksum)
	if checksum == "" {
		return ctx
	}
	return context.WithValue(ctx, documentKey{}, checksum)
}

// WithDocumentRecord marks ctx as being about document. The one place that
// knows the header's value is the checksum and never the record id, so a
// caller holding a documents row cannot reach for the wrong field.
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

// managed is AI_MANAGED, read once at wiring time by config.NewRuntime.
//
// It lives here rather than being threaded through every constructor because
// the two places that need it are the deepest ones: the Anthropic client the
// opencode SDK builds inside ai.NewOpenAIClient, and the hand-built OCR
// requests in internal/ocr. Threading a bool to both would have put a
// positional parameter on nine constructors and every call site that has ever
// built one in a test, to carry a value that is constant for the process.
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
func DocumentOptions() []option.RequestOption {
	if !Managed() {
		return nil
	}
	return []option.RequestOption{option.WithMiddleware(documentMiddleware)}
}

// documentMiddleware stamps the document checksum from the request context. Like
// SessionMiddleware it runs per attempt on a request clone, so retries and the
// /responses fallback are stamped too, and it stamps unconditionally because
// DocumentOptions only installs it in managed mode.
func documentMiddleware(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	StampDocument(req)
	return next(req)
}

// StampDocument sets DocumentHeader on a request built by hand -- the Mistral
// and Docling OCR calls, which speak neither SDK. It is a no-op unless this is
// the managed deployment and the context names a document.
func StampDocument(req *http.Request) {
	if req == nil || !Managed() {
		return
	}
	if checksum := DocumentFrom(req.Context()); checksum != "" {
		req.Header.Set(DocumentHeader, checksum)
	}
}
