package appapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

type stubSweeper struct {
	started int
	running bool
}

func (s *stubSweeper) StartSweep() bool {
	s.started++
	return !s.running
}

func (s *stubSweeper) SweepRunning() bool { return s.running }

// Nothing to embed with means nothing to start, and the admin has to be told
// where the binding is made -- Management has no model picker of its own.
func TestEmbeddingBackfillRefusesWithNoModelBound(t *testing.T) {
	t.Parallel()
	sweeper := &stubSweeper{}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Response = rec
	e.Request = httptest.NewRequest(http.MethodPost, "/api/app/embeddings/backfill", nil)

	// A nil app is the assertion that nothing is scanned: the refusal happens
	// before any database work.
	if err := handlePostEmbeddingBackfill(nil, &config.Runtime{}, sweeper)(e); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), "Settings") {
		t.Fatalf("the refusal must point at Settings, got %q", rec.Body.String())
	}
	if sweeper.started != 0 {
		t.Fatalf("started %d sweeps with no model bound", sweeper.started)
	}
}
