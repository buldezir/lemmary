package appwire

import (
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"

	"lemmary/backend/internal/metrics"
)

// registerRequestMetrics times every request and records what the client
// actually got back.
//
// Hand-rolled rather than otelhttp's ready-made middleware, and the reason is
// the one thing a request metric exists for -- the status code. PocketBase's
// router calls its handler chain and then, if that chain returned an error,
// writes the response itself in router.ErrorHandler *after* the chain has
// unwound (tools/router/router.go, the mux.HandleFunc body). Any wrapper bound
// into the chain therefore sees a response nobody has written yet and defaults
// it to 200: `return e.UnauthorizedError(...)` from authguard, a not-found, a
// bad request -- every one of them lands on status 200, and a scrape of the
// 401 rate reads as perfectly healthy. Wrapping outside the mux instead would
// see the real status but lose http.route, because ServeMux sets the pattern
// on the request it passes inward.
//
// What a hook on the chain does have is the error, so the status can be
// derived from it with the same mapping ErrorHandler is about to apply.
//
// PocketBase's own realtime route is skipped: that handler holds the
// connection open for the life of the SSE stream, so each one would land a
// sample of minutes in a histogram whose top bucket is ten seconds.
func registerRequestMetrics(app core.App) {
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: -9999,
		Func: func(e *core.ServeEvent) error {
			e.Router.Bind(&hook.Handler[*core.RequestEvent]{
				Id: "lemmaryRequestMetrics",
				// One inside vault's inflight wrapper (-99999), which has to
				// stay the outermost thing on the chain, and outside
				// everything else so the time covers auth and rate limiting
				// too.
				Priority: -99998,
				Func:     recordRequest,
			})
			return e.Next()
		},
	})
}

func recordRequest(e *core.RequestEvent) error {
	if e.Request.Pattern == realtimePattern {
		return e.Next()
	}
	start := time.Now()
	err := e.Next()
	metrics.HTTPRequest(
		e.Request.Method,
		routeOf(e.Request),
		responseStatus(e, err),
		time.Since(start),
	)
	return err
}

const realtimePattern = "GET /api/realtime"

// responseStatus is the status the client will see.
//
// Whatever a handler already wrote wins, because ErrorHandler returns without
// writing when the response has been started -- a stream that fails halfway
// still answered 200. Only then does the error decide: router.ToApiError is
// exactly what ErrorHandler uses, so an error maps here the way it will map
// there, including the errors that are not ApiErrors at all and become a 400
// or a 500 on the way out.
func responseStatus(e *core.RequestEvent, err error) int {
	if status := e.Status(); status != 0 {
		return status
	}
	if err != nil {
		return router.ToApiError(err).Status
	}
	// A handler that wrote nothing and returned nothing: net/http sends 200.
	return http.StatusOK
}

// routeOf is the matched pattern, never the raw path -- a label carrying
// document ids would be a series per document.
//
// PocketBase registers its routes as "GET /api/collections/{collection}" and
// ServeMux hands that whole string back, method included. The method is
// already its own attribute, so it comes off here.
func routeOf(r *http.Request) string {
	pattern := r.Pattern
	if _, path, found := strings.Cut(pattern, " "); found {
		return path
	}
	if pattern == "" {
		// No pattern means nothing matched, and there is no route to name. A
		// constant keeps that from becoming a series per unmatched URL.
		return "unmatched"
	}
	return pattern
}
