// Package importjob keeps an in-memory registry of background import runs.
// Jobs live for the process lifetime only; a restart loses them.
package importjob

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Job statuses reported by the status endpoints.
const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// DefaultRetention is how long a finished job stays readable before it is swept.
// Without it the registry would grow for the process lifetime.
const DefaultRetention = time.Hour

// ErrBusy is returned when the owner already has an import running.
var ErrBusy = errors.New("an import is already in progress")

// Progress is the coarse "done of total" counter a long run may report.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Job is a snapshot of one import run.
type Job[T any] struct {
	ID          string    `json:"job_id"`
	OwnerUserID string    `json:"-"`
	Status      string    `json:"status"`
	Progress    Progress  `json:"progress"`
	Result      *T        `json:"result,omitempty"`
	Error       string    `json:"error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Registry tracks jobs carrying result type T and allows one run per owner.
type Registry[T any] struct {
	retention time.Duration

	mu   sync.Mutex
	jobs map[string]*Job[T]
	busy map[string]struct{}
}

// NewRegistry returns an empty registry. A retention <= 0 uses DefaultRetention.
func NewRegistry[T any](retention time.Duration) *Registry[T] {
	if retention <= 0 {
		retention = DefaultRetention
	}
	return &Registry[T]{
		retention: retention,
		jobs:      map[string]*Job[T]{},
		busy:      map[string]struct{}{},
	}
}

// Acquire marks the owner busy so a second run cannot start. Callers that run
// synchronously (without Start) must pair it with Release.
func (r *Registry[T]) Acquire(ownerUserID string) error {
	if strings.TrimSpace(ownerUserID) == "" {
		return fmt.Errorf("owner user id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.busy[ownerUserID]; busy {
		return ErrBusy
	}
	r.busy[ownerUserID] = struct{}{}
	return nil
}

// Release clears the owner's busy marker.
func (r *Registry[T]) Release(ownerUserID string) {
	r.mu.Lock()
	delete(r.busy, ownerUserID)
	r.mu.Unlock()
}

// Start runs fn in a background goroutine and returns the new job id.
// fn receives a reporter it may call to publish progress; the reported result
// is stored even when fn fails, so partial counts stay visible.
func (r *Registry[T]) Start(ownerUserID string, fn func(report func(done, total int)) (T, error)) (string, error) {
	if err := r.Acquire(ownerUserID); err != nil {
		return "", err
	}

	id, err := newJobID()
	if err != nil {
		r.Release(ownerUserID)
		return "", err
	}

	now := time.Now().UTC()
	job := &Job[T]{
		ID:          id,
		OwnerUserID: ownerUserID,
		Status:      StatusRunning,
		UpdatedAt:   now,
	}
	r.mu.Lock()
	r.pruneLocked(now)
	r.jobs[id] = job
	r.mu.Unlock()

	go func() {
		// Registered first so it unlocks after the mutex defer below.
		defer r.Release(ownerUserID)

		result, runErr := r.runProtected(job, fn)

		// Fields are written under r.mu; Get copies, so readers never race.
		r.mu.Lock()
		defer r.mu.Unlock()
		job.UpdatedAt = time.Now().UTC()
		job.Result = &result
		if runErr != nil {
			job.Status = StatusFailed
			job.Error = runErr.Error()
			return
		}
		job.Status = StatusCompleted
	}()

	return id, nil
}

// runProtected invokes fn, converting a panic into a job failure: the Start
// goroutine has no supervisor, so an unrecovered panic in an import would take
// down the whole server (and every in-flight processing job with it).
func (r *Registry[T]) runProtected(job *Job[T], fn func(report func(done, total int)) (T, error)) (result T, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("import panicked: %v", p)
		}
	}()
	return fn(func(done, total int) {
		r.mu.Lock()
		job.Progress = Progress{Done: done, Total: total}
		job.UpdatedAt = time.Now().UTC()
		r.mu.Unlock()
	})
}

// Get returns a copy of the job, or false if unknown.
func (r *Registry[T]) Get(id string) (Job[T], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job == nil {
		return Job[T]{}, false
	}
	out := *job
	if job.Result != nil {
		copied := *job.Result
		out.Result = &copied
	}
	return out, true
}

// pruneLocked drops finished jobs older than the retention window.
// Callers must hold r.mu. Running jobs are never swept.
func (r *Registry[T]) pruneLocked(now time.Time) {
	for id, job := range r.jobs {
		if job == nil {
			delete(r.jobs, id)
			continue
		}
		if job.Status == StatusRunning {
			continue
		}
		if now.Sub(job.UpdatedAt) > r.retention {
			delete(r.jobs, id)
		}
	}
}

func newJobID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
