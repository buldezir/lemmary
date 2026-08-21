package ngxapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/fulltext"
)

type paginatedResponse struct {
	Count    int64   `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []any   `json:"results"`
}

func writeJSON(e *core.RequestEvent, status int, data any) error {
	if err := checkAPIVersion(e); err != nil {
		return err
	}
	setNgxHeaders(e)
	e.Response.Header().Set("Content-Type", "application/json")
	if e.Request.Method == http.MethodHead {
		e.Response.WriteHeader(status)
		return nil
	}
	e.Response.WriteHeader(status)
	return json.NewEncoder(e.Response).Encode(data)
}

func badRequest(e *core.RequestEvent, detail string) error {
	return writeJSON(e, http.StatusBadRequest, map[string]any{"detail": detail})
}

func unauthorized(e *core.RequestEvent, detail string) error {
	return writeJSON(e, http.StatusUnauthorized, map[string]any{"detail": detail})
}

func notFound(e *core.RequestEvent, detail string) error {
	return writeJSON(e, http.StatusNotFound, map[string]any{"detail": detail})
}

// internalError logs the cause and returns a generic 500 so upstream/database
// details never reach the client.
func internalError(e *core.RequestEvent, err error) error {
	if e.App != nil {
		e.App.Logger().Error("ngx api request failed",
			"path", e.Request.URL.Path,
			slog.Any("error", err),
		)
	}
	return writeJSON(e, http.StatusInternalServerError, map[string]any{"detail": "Internal server error."})
}

func methodNotAllowed(e *core.RequestEvent, allowed string) error {
	if err := checkAPIVersion(e); err != nil {
		return err
	}
	setNgxHeaders(e)
	e.Response.Header().Set("Allow", allowed)
	if e.Request.Method == http.MethodHead {
		e.Response.WriteHeader(http.StatusMethodNotAllowed)
		return nil
	}
	e.Response.Header().Set("Content-Type", "application/json")
	e.Response.WriteHeader(http.StatusMethodNotAllowed)
	return json.NewEncoder(e.Response).Encode(map[string]string{
		"detail": fmt.Sprintf(`Method "%s" not allowed.`, e.Request.Method),
	})
}

func requestBaseURL(e *core.RequestEvent) string {
	scheme := "http"
	if e.IsTLS() {
		scheme = "https"
	}
	if proto := e.Request.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return fmt.Sprintf("%s://%s", scheme, e.Request.Host)
}

func paginationParams(e *core.RequestEvent) (page, pageSize int) {
	page = 1
	pageSize = 25

	if v := e.Request.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := e.Request.URL.Query().Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pageSize = n
		}
	}
	return page, pageSize
}

func clampSearchPageSize(pageSize int) int {
	if pageSize > fulltext.MaxSearchLimit {
		return fulltext.MaxSearchLimit
	}
	if pageSize <= 0 {
		return 25
	}
	return pageSize
}

func buildPageURL(e *core.RequestEvent, page int) string {
	q := e.Request.URL.Query()
	q.Set("page", strconv.Itoa(page))
	return requestBaseURL(e) + e.Request.URL.Path + "?" + q.Encode()
}

func paginatedList(e *core.RequestEvent, total int64, page, pageSize int, results []any) error {
	var next, prev *string
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if page < totalPages {
		u := buildPageURL(e, page+1)
		next = &u
	}
	if page > 1 {
		u := buildPageURL(e, page-1)
		prev = &u
	}
	if results == nil {
		results = []any{}
	}
	return writeJSON(e, http.StatusOK, paginatedResponse{
		Count:    total,
		Next:     next,
		Previous: prev,
		Results:  results,
	})
}

func handleEmptyList(e *core.RequestEvent) error {
	page, pageSize := paginationParams(e)
	return paginatedList(e, 0, page, pageSize, []any{})
}
