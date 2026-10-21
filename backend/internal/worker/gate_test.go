package worker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
)

func serverAnswering(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

func pingConfig(url string) config.Config {
	return config.Config{
		ProcessingPing:  true,
		ExtractProvider: &aiprovider.Provider{SDK: aiprovider.SDKOpenAI, BaseURL: url},
	}
}

// A window that cannot contain the present, whatever the clock says.
func closedWindow() config.ProcessingWindow {
	now := time.Now().UTC()
	return config.ProcessingWindow{
		Enabled: true,
		Start:   now.Add(time.Hour).Format("15:04"),
		End:     now.Add(2 * time.Hour).Format("15:04"),
	}
}

func gateFor(cfg config.Config) *Gate {
	return &Gate{snapshot: func() config.Snapshot { return config.Snapshot{Cfg: cfg} }}
}

func TestPingCountsAnyAnswerButAProxysAsUp(t *testing.T) {
	t.Parallel()

	for status, up := range map[int]bool{
		http.StatusOK:                 true,
		http.StatusNotFound:           true,
		http.StatusUnauthorized:       true,
		http.StatusBadGateway:         false,
		http.StatusServiceUnavailable: false,
		http.StatusGatewayTimeout:     false,
	} {
		srv, _ := serverAnswering(t, status)
		if err := ping(pingConfig(srv.URL)); (err == nil) != up {
			t.Errorf("status %d: ping error %v, want up=%v", status, err, up)
		}
	}

	srv, _ := serverAnswering(t, http.StatusOK)
	srv.Close()
	if err := ping(pingConfig(srv.URL)); err == nil {
		t.Error("a server that is gone answered the ping")
	}
}

func TestPingAsksEachServerOnce(t *testing.T) {
	t.Parallel()
	srv, hits := serverAnswering(t, http.StatusOK)
	cfg := pingConfig(srv.URL)
	cfg.OCRProvider = cfg.ExtractProvider
	cfg.EmbeddingProvider = &aiprovider.Provider{SDK: aiprovider.SDKLocalEmbeddings, BaseURL: srv.URL}

	if err := ping(cfg); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("one server pinged %d times", n)
	}
}

func TestGateReusesTheLastPingUnlessFresh(t *testing.T) {
	t.Parallel()
	srv, hits := serverAnswering(t, http.StatusOK)
	g := gateFor(pingConfig(srv.URL))

	for range 3 {
		if hold := g.Hold(false); hold != "" {
			t.Fatalf("Hold = %q with the server up", hold)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("pinged %d times inside a minute", n)
	}
	g.Hold(true)
	if n := hits.Load(); n != 2 {
		t.Fatalf("a fresh Hold pinged %d times in all, want 2", n)
	}
}

func TestGateHoldsWhileTheServerIsDown(t *testing.T) {
	t.Parallel()
	srv, _ := serverAnswering(t, http.StatusServiceUnavailable)
	g := gateFor(pingConfig(srv.URL))
	g.app = bootAppForEnqueue(t)

	if g.Status() != "" {
		t.Fatal("Status held before anything was pinged")
	}
	if hold := g.Hold(false); hold != HoldOffline {
		t.Fatalf("Hold = %q, want %q", hold, HoldOffline)
	}
	if hold := g.Status(); hold != HoldOffline {
		t.Fatalf("Status = %q after a failed ping, want %q", hold, HoldOffline)
	}
}

func TestGateOutsideTheWindowDoesNotPing(t *testing.T) {
	t.Parallel()
	srv, hits := serverAnswering(t, http.StatusOK)
	cfg := pingConfig(srv.URL)
	cfg.ProcessingWindow = closedWindow()
	g := gateFor(cfg)

	if hold := g.Hold(true); hold != HoldSchedule {
		t.Fatalf("Hold = %q, want %q", hold, HoldSchedule)
	}
	if hits.Load() != 0 {
		t.Fatal("pinged a server the schedule says is off")
	}
}

func TestGateOffNeverHolds(t *testing.T) {
	t.Parallel()
	srv, hits := serverAnswering(t, http.StatusServiceUnavailable)
	cfg := pingConfig(srv.URL)
	cfg.ProcessingPing = false

	var none *Gate
	for name, g := range map[string]*Gate{"nil": none, "off": gateFor(cfg)} {
		if hold := g.Hold(true); hold != "" {
			t.Errorf("%s gate: Hold = %q", name, hold)
		}
		if hold := g.Status(); hold != "" {
			t.Errorf("%s gate: Status = %q", name, hold)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("pinged with the ping off")
	}
}

func TestDrainLeavesHeldJobsPending(t *testing.T) {
	app := bootAppForEnqueue(t)
	userID := makeUserForDrain(t, app, "held@example.test")
	job := makeOCRJob(t, app, makeImageDocument(t, app, userID, "Held"))

	provider := &slowOCR{}
	p := newDrainProcessor(app, 1, provider)
	p.gate = gateFor(config.Config{ProcessingWindow: closedWindow()})
	p.fanOut()

	if _, calls := provider.stats(); calls != 0 {
		t.Fatalf("OCR called %d times outside the window", calls)
	}
	reloaded, err := app.FindRecordById("processing_jobs", job.Id)
	if err != nil {
		t.Fatal(err)
	}
	if status := reloaded.GetString("status"); status != models.JobStatusPending || reloaded.GetString("started_at") != "" {
		t.Fatalf("held job status %q, started_at %q; want it untouched", status, reloaded.GetString("started_at"))
	}
}

type failingOCR struct{}

func (failingOCR) Name() string { return "failing-ocr" }

func (failingOCR) ExtractText(context.Context, string, string) (string, error) {
	return "", errors.New("connection refused")
}

// runHeldFailure claims the job the way runJob does and runs it against an OCR
// that always fails, with the gate held or not.
func runHeldFailure(t *testing.T, app core.App, jobID string, held bool) *core.Record {
	t.Helper()
	job, err := app.FindRecordById("processing_jobs", jobID)
	if err != nil {
		t.Fatal(err)
	}
	job.Set("status", models.JobStatusRunning)
	job.Set("started_at", nowTimestamp())
	if err := app.Save(job); err != nil {
		t.Fatal(err)
	}
	runner := NewPipelineRunner(app, config.Config{OCRTimeout: 10 * time.Second, WorkerTimeout: 30 * time.Second},
		failingOCR{}, stubExtractor{}, nil, RelatedLinking{})
	runner.held = func() bool { return held }
	_ = runner.Run(context.Background(), jobID)

	reloaded, err := app.FindRecordById("processing_jobs", jobID)
	if err != nil {
		t.Fatal(err)
	}
	return reloaded
}

func TestStepFailureWithoutAHoldFails(t *testing.T) {
	app := bootAppForEnqueue(t)
	job := makeOCRJob(t, app, makeImageDocument(t, app, makeUserForDrain(t, app, "unheld@example.test"), "Unheld"))

	if status := runHeldFailure(t, app, job.Id, false).GetString("status"); status != models.JobStatusFailed {
		t.Fatalf("status %q, want failed", status)
	}
}

// The window closing or the server going away under a step is not the
// document's fault: it waits for the gate instead of failing or spending a
// retry, and reads as waiting rather than as a retry in progress.
func TestStepFailureWhileHeldRequeuesWithoutSpendingARetry(t *testing.T) {
	app := bootAppForEnqueue(t)
	job := makeOCRJob(t, app, makeImageDocument(t, app, makeUserForDrain(t, app, "cutoff@example.test"), "Cut off"))

	reloaded := runHeldFailure(t, app, job.Id, true)
	if status := reloaded.GetString("status"); status != models.JobStatusPending {
		t.Fatalf("status %q, want pending", status)
	}
	if at := reloaded.GetString("next_attempt_at"); at != "" {
		t.Fatalf("held job backed off to %s; the gate decides when it runs", at)
	}
	if at := reloaded.GetString("started_at"); at != "" {
		t.Fatalf("held job kept started_at %s, so it reads as running", at)
	}
	runs, err := parseStepRuns(reloaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Attempts != 0 || runs[0].Status != models.StepStatusPending || runs[0].Error != "" || !runs[0].Held {
		t.Fatalf("step runs %+v, want one pending OCR run, held, with no attempt spent", runs)
	}
	document, err := app.FindRecordById("documents", reloaded.GetString("document"))
	if err != nil {
		t.Fatal(err)
	}
	if status := document.GetString("processing_status"); status != models.DocStatusPending {
		t.Fatalf("document status %q, want pending", status)
	}
}

// A document whose step takes the server down with it fails the probe every
// time. Refunded for ever it would head the queue and starve everything
// behind it, so only the first held failure is free.
func TestASecondHeldFailureCounts(t *testing.T) {
	app := bootAppForEnqueue(t)
	job := makeOCRJob(t, app, makeImageDocument(t, app, makeUserForDrain(t, app, "crashloop@example.test"), "Crash loop"))

	runHeldFailure(t, app, job.Id, true)
	if status := runHeldFailure(t, app, job.Id, true).GetString("status"); status != models.JobStatusFailed {
		t.Fatalf("second held failure left the job %q, want failed", status)
	}
}
