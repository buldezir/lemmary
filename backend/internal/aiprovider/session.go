package aiprovider

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/openai/openai-go/option"
)

// SessionHeader groups requests that share a prompt prefix. OpenCode Go uses it
// to route them onto the same prompt cache, and requests that arrive without it
// are liable to be refused outright.
const SessionHeader = "x-opencode-session"

// sessionNamespace seeds the derived per-purpose ids. Any fixed UUID does; this
// one is arbitrary and must not change, or every deployment's purpose ids move
// at once.
var sessionNamespace = uuid.MustParse("6f1c9d1e-0b6a-4c4b-9d5a-2f3c8e7a41d0")

type sessionKey struct{}

// WithSession marks ctx as belonging to one conversation. Every provider
// request made under it carries that id.
func WithSession(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKey{}, id)
}

// SessionFrom returns the conversation id on ctx, or "" when there is none.
func SessionFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(sessionKey{}).(string)
	return id
}

// EnsureSession keeps an id already on ctx and otherwise falls back to
// SessionFor(purpose). Callers with a real conversation set it first, so this
// only ever fills in for background work -- and a call site nobody wired still
// sends a usable key rather than none.
func EnsureSession(ctx context.Context, purpose string) context.Context {
	if SessionFrom(ctx) != "" {
		return ctx
	}
	return WithSession(ctx, SessionFor(purpose))
}

var (
	processSession = uuid.NewString()

	purposeSessionsMu sync.Mutex
	purposeSessions   = map[string]string{}
)

// SessionFor is one stable id per purpose for the life of the process. The
// system prompt behind "extract" or "ocr" is identical from one document to the
// next, so grouping those requests is exactly what the header is for; a fresh
// id per document would throw the cache away every time.
func SessionFor(purpose string) string {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return processSession
	}

	purposeSessionsMu.Lock()
	defer purposeSessionsMu.Unlock()
	if id, ok := purposeSessions[purpose]; ok {
		return id
	}
	id := uuid.NewSHA1(sessionNamespace, []byte(processSession+"/"+purpose)).String()
	purposeSessions[purpose] = id
	return id
}

// SessionHost reports whether the header means anything to this host. Only
// OpenCode asks for it; every other provider sees the request it saw before.
func SessionHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	return h == "opencode.ai" || strings.HasSuffix(h, ".opencode.ai")
}

// SessionMiddleware stamps the session id from the request context onto
// outbound OpenCode requests. It sits in the SDK's middleware chain, which runs
// per attempt on a request clone, so retries are stamped too.
func SessionMiddleware() option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if req != nil && req.URL != nil && SessionHost(req.URL.Host) {
			if id := SessionFrom(req.Context()); id != "" {
				req.Header.Set(SessionHeader, id)
			}
		}
		return next(req)
	}
}

// RewriteHostMiddleware sends the request to host instead of req.URL.Host.
// Tests register it after SessionMiddleware so a client whose base URL is
// opencode.ai (the session gate opens) still talks to an httptest server
// rather than the internet. Production callers never pass it.
func RewriteHostMiddleware(host string) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if req != nil && req.URL != nil {
			req.URL.Host = host
		}
		return next(req)
	}
}
