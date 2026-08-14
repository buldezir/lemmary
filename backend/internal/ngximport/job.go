package ngximport

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Job statuses for in-memory async imports.
const (
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
)

// Job is an in-memory import run snapshot (lost on process restart).
type Job struct {
	ID          string    `json:"job_id"`
	OwnerUserID string    `json:"-"`
	Status      string    `json:"status"`
	Result      *Result   `json:"result,omitempty"`
	Error       string    `json:"error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var (
	jobsMu     sync.Mutex
	jobs       = map[string]*Job{}
	importMu   sync.Mutex
	importBusy = map[string]struct{}{}
)

func acquireImport(ownerUserID string) error {
	if strings.TrimSpace(ownerUserID) == "" {
		return fmt.Errorf("owner user id is required")
	}
	importMu.Lock()
	defer importMu.Unlock()
	if _, busy := importBusy[ownerUserID]; busy {
		return ErrImportInProgress
	}
	importBusy[ownerUserID] = struct{}{}
	return nil
}

func releaseImport(ownerUserID string) {
	importMu.Lock()
	delete(importBusy, ownerUserID)
	importMu.Unlock()
}

// Start begins an import in a background goroutine and returns the job id.
// Only one import may run at a time per owner.
func Start(app core.App, ownerUserID, baseURL, apiKey, mode string) (string, error) {
	if err := acquireImport(ownerUserID); err != nil {
		return "", err
	}

	id, err := newJobID()
	if err != nil {
		releaseImport(ownerUserID)
		return "", err
	}
	job := &Job{
		ID:          id,
		OwnerUserID: ownerUserID,
		Status:      JobStatusRunning,
		UpdatedAt:   time.Now().UTC(),
	}
	jobsMu.Lock()
	jobs[id] = job
	jobsMu.Unlock()

	go func() {
		defer releaseImport(ownerUserID)
		result, runErr := runImport(app, ownerUserID, baseURL, apiKey, mode, nil)
		jobsMu.Lock()
		defer jobsMu.Unlock()
		job.UpdatedAt = time.Now().UTC()
		if runErr != nil {
			job.Status = JobStatusFailed
			job.Error = runErr.Error()
			job.Result = &result
			return
		}
		job.Status = JobStatusCompleted
		job.Result = &result
	}()

	return id, nil
}

// GetJob returns a copy of the in-memory job, or false if unknown.
func GetJob(id string) (Job, bool) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	job, ok := jobs[id]
	if !ok || job == nil {
		return Job{}, false
	}
	out := *job
	if job.Result != nil {
		copied := *job.Result
		out.Result = &copied
	}
	return out, true
}

func newJobID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
