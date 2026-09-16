package worker

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
)

// A 1x1 PNG: the OCR step hands text and office formats to textextract without
// reaching the provider, so a call-counting stub needs an image.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// The high-water mark of concurrent calls should sit at the configured limit,
// never above it.
type slowOCR struct {
	delay time.Duration

	mu    sync.Mutex
	now   int
	peak  int
	calls int
}

func (s *slowOCR) Name() string { return "slow-ocr" }

func (s *slowOCR) ExtractText(ctx context.Context, _ string, _ string) (string, error) {
	s.mu.Lock()
	s.now++
	s.calls++
	if s.now > s.peak {
		s.peak = s.now
	}
	s.mu.Unlock()

	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}

	s.mu.Lock()
	s.now--
	s.mu.Unlock()
	return "ocr text", nil
}

func (s *slowOCR) stats() (peak, calls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak, s.calls
}

func makeImageDocument(t *testing.T, app core.App, userID, title string) string {
	t.Helper()
	png, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	document := core.NewRecord(documents)
	document.Set("user", userID)
	document.Set("title", title)
	file, err := filesystem.NewFileFromBytes(png, "page.png")
	if err != nil {
		t.Fatalf("build file: %v", err)
	}
	document.Set("file", file)
	if err := app.Save(document); err != nil {
		t.Fatalf("save document: %v", err)
	}
	return document.Id
}

func makeUserForDrain(t *testing.T, app core.App, email string) string {
	t.Helper()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	user := core.NewRecord(users)
	user.Set("email", email)
	user.SetPassword("test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return user.Id
}

func makeOCRJob(t *testing.T, app core.App, documentID string) *core.Record {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatalf("processing_jobs collection: %v", err)
	}
	job := core.NewRecord(collection)
	job.Set("document", documentID)
	job.Set("status", models.JobStatusPending)
	job.Set("steps", []string{models.StepOCR})
	if err := app.Save(job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	return job
}

func newDrainProcessor(app core.App, limit int, provider *slowOCR) *Processor {
	snap := config.Snapshot{
		Cfg: config.Config{
			OCRTimeout:    10 * time.Second,
			WorkerTimeout: 30 * time.Second,
		},
		OCR: provider,
		AI:  stubExtractor{},
	}
	return &Processor{
		app:      app,
		limit:    limit,
		inflight: make(map[string]struct{}),
		snapshot: func() config.Snapshot { return snap },
	}
}

// The drain used to hold a process-wide mutex, so eight documents took eight
// OCR round trips back to back. Now they finish in roughly a quarter of the
// time, each exactly once, never more than the configured four at a time.
func TestDrainPendingRunsJobsConcurrently(t *testing.T) {
	const (
		jobs  = 8
		limit = 4
		delay = 150 * time.Millisecond
	)

	app := bootAppForEnqueue(t)
	userID := makeUserForDrain(t, app, "drain@example.test")

	ids := make([]string, 0, jobs)
	for i := 0; i < jobs; i++ {
		documentID := makeImageDocument(t, app, userID, fmt.Sprintf("Drain %d", i))
		ids = append(ids, makeOCRJob(t, app, documentID).Id)
	}

	provider := &slowOCR{delay: delay}
	p := newDrainProcessor(app, limit, provider)

	start := time.Now()
	p.fanOut()
	elapsed := time.Since(start)

	peak, calls := provider.stats()
	if calls != jobs {
		t.Fatalf("provider called %d times, want %d (a double-claim runs a document twice)", calls, jobs)
	}
	if peak > limit {
		t.Fatalf("peak concurrency %d exceeds limit %d", peak, limit)
	}
	if peak < 2 {
		t.Fatalf("peak concurrency %d: jobs still ran one at a time", peak)
	}
	// Serial would be 8 delays; slack is generous for a loaded CI box.
	if serial := jobs * delay; elapsed > serial*3/4 {
		t.Fatalf("drained in %s, want well under the serial %s", elapsed, serial)
	}

	for _, id := range ids {
		job, err := app.FindRecordById("processing_jobs", id)
		if err != nil {
			t.Fatalf("load job %s: %v", id, err)
		}
		if job.GetString("finished_at") == "" {
			t.Fatalf("job %s did not reach a terminal state (status %q, error %q)",
				id, job.GetString("status"), job.GetString("error"))
		}
	}

	p.mu.Lock()
	left := len(p.inflight)
	active := p.active
	p.mu.Unlock()
	if left != 0 || active != 0 {
		t.Fatalf("drain leaked state: %d jobs in flight, %d slots held", left, active)
	}
}

// Without this, every drain goroutine selects the same globally oldest row and
// the losers spin until the no-progress guard parks them.
func TestNextDueJobSkipsInflight(t *testing.T) {
	app := bootAppForEnqueue(t)
	userID := makeUserForDrain(t, app, "nextdue@example.test")

	first := makeOCRJob(t, app, makeImageDocument(t, app, userID, "First"))
	second := makeOCRJob(t, app, makeImageDocument(t, app, userID, "Second"))

	p := newDrainProcessor(app, 4, &slowOCR{})

	picked, err := p.nextDueJob()
	if err != nil {
		t.Fatalf("next due job: %v", err)
	}
	if picked == nil || picked.Id != first.Id {
		t.Fatalf("expected the oldest job %s, got %v", first.Id, picked)
	}

	next, err := p.nextDueJob()
	if err != nil {
		t.Fatalf("next due job: %v", err)
	}
	if next == nil || next.Id != second.Id {
		t.Fatalf("expected the second job %s, got %v", second.Id, next)
	}

	p.releaseJob(first.Id)
	p.releaseJob(second.Id)
	again, err := p.nextDueJob()
	if err != nil {
		t.Fatalf("next due job: %v", err)
	}
	if again == nil || again.Id != first.Id {
		t.Fatalf("a released job should be pickable again, got %v", again)
	}
}
