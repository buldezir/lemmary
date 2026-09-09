package worker

import (
	"encoding/json"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
)

// jobOverridesField is the column a job's provider/model choices live in.
const jobOverridesField = "overrides"

// parseJobOverrides reads a job's provider and model choices.
//
// A job with nothing stored, or with something unreadable stored, runs on the
// configured bindings -- the same answer parseForceSteps gives, and for the
// same reason: the overrides are a refinement of a job that is otherwise
// perfectly runnable, and refusing to run it over a malformed refinement would
// strand a document. Anything a browser could put there has already been
// refused by the create hook.
func parseJobOverrides(job *core.Record) config.Overrides {
	var out config.Overrides
	raw := job.Get(jobOverridesField)
	if raw == nil {
		return out
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return out
	}
	// A JSONField round-trips an empty column as the four bytes "null", which
	// unmarshals into the zero value anyway; decoding it is cheaper than
	// special-casing it.
	if err := json.Unmarshal(data, &out); err != nil {
		return config.Overrides{}
	}
	return out
}

// setJobOverrides writes the choices onto a new job, leaving the column unset
// when there are none. Unset rather than "{}" so a job queued without a picker
// touched is byte-identical to every job created before overrides existed.
func setJobOverrides(job *core.Record, overrides config.Overrides) {
	if overrides.Empty() {
		return
	}
	job.Set(jobOverridesField, overrides)
}

// effectiveSnapshot is the configuration this job actually runs under: the
// published snapshot, with whatever the job overrides rebuilt on its own
// bindings.
//
// A job with no overrides gets the published snapshot unchanged and builds
// nothing, which is every job an unmodified reprocess queues.
func (p *Processor) effectiveSnapshot(job *core.Record) (config.Snapshot, error) {
	overrides := parseJobOverrides(job)
	if overrides.Empty() {
		return p.rt.Snapshot(), nil
	}
	return p.rt.WithOverrides(p.app, overrides)
}

// validateJobOverrides refuses a job whose overrides name a provider that does
// not exist or cannot do the work.
//
// It runs in the processing_jobs create hook rather than in the reprocess
// handler because that collection is writable by its owner: the single-document
// reprocess form creates a job straight through the collection API
// (frontend/src/lib/api/documents.ts), so a check in the custom endpoint would
// guard one of the two ways a job is made. This is the point both paths cross.
func validateJobOverrides(app core.App, cfg config.Config, job *core.Record) error {
	overrides := parseJobOverrides(job)
	if overrides.Empty() {
		return nil
	}
	// Chat and search are not refused here, only unused. This hook guards what
	// a job may run on; the reprocess endpoint is where a client that sent an
	// inapplicable binding is told so, because it is the layer that knows the
	// request was a reprocess rather than an upload.
	return overrides.Validate(app, cfg)
}
