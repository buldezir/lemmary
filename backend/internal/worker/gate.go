package worker

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

// What holds the queue, as /meta reports it.
const (
	HoldSchedule = "schedule"
	HoldOffline  = "offline"
)

const (
	pingEvery   = time.Minute
	pingTimeout = 5 * time.Second
)

// Gate keeps jobs pending outside the processing window and, with the ping on,
// while a bound processing provider does not answer: for an AI server that is
// not up around the clock. Nil is a gate that never holds.
type Gate struct {
	app      core.App
	snapshot func() config.Snapshot

	// Serializes pings, so a burst of drains asks the server once.
	mu        sync.Mutex
	checkedAt time.Time
	down      atomic.Bool
}

func NewGate(app core.App, rt *config.Runtime) *Gate {
	return &Gate{app: app, snapshot: rt.Snapshot}
}

// Hold names what keeps jobs from starting now, or "" when nothing does. It
// pings when the last answer is a minute old, or at once when fresh is set,
// which is for a step that just failed and may have failed because the server
// went away.
func (g *Gate) Hold(fresh bool) string {
	if g == nil {
		return ""
	}
	cfg := g.snapshot().Cfg
	if cfg.ProcessingPing && cfg.ProcessingWindow.Contains(time.Now()) {
		g.refresh(cfg, fresh)
	}
	return g.status(cfg)
}

// Status is Hold on what is already known. It never pings, so a page load
// cannot be turned into traffic to the AI server.
func (g *Gate) Status() string {
	if g == nil {
		return ""
	}
	return g.status(g.snapshot().Cfg)
}

func (g *Gate) status(cfg config.Config) string {
	switch {
	case !cfg.ProcessingWindow.Contains(time.Now()):
		return HoldSchedule
	case cfg.ProcessingPing && g.down.Load():
		return HoldOffline
	}
	return ""
}

func (g *Gate) refresh(cfg config.Config, fresh bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !fresh && !g.checkedAt.IsZero() && time.Since(g.checkedAt) < pingEvery {
		return
	}
	err := ping(cfg)
	g.checkedAt = time.Now()
	if down := err != nil; g.down.Swap(down) != down {
		if down {
			g.app.Logger().Warn("AI server not answering; holding pending jobs", slog.Any("error", err))
		} else {
			g.app.Logger().Info("AI server answering again; resuming pending jobs")
		}
	}
}

// ping reaches every provider a job talks to. Any HTTP answer counts, a 404
// on a bare /v1 included: what is asked is whether the machine is up, not
// whether this URL means anything. Except the three a reverse proxy gives for
// a backend that is asleep.
func ping(cfg config.Config) error {
	seen := map[string]bool{}
	for _, p := range []*aiprovider.Provider{cfg.OCRProvider, cfg.ExtractProvider, cfg.EmbeddingProvider} {
		if p == nil || p.BaseURL == "" || seen[p.BaseURL] {
			continue
		}
		seen[p.BaseURL] = true
		if err := reach(p.BaseURL); err != nil {
			return err
		}
	}
	return nil
}

func reach(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return nil
}
