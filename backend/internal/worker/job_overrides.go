package worker

import (
	"encoding/json"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
)

const jobOverridesField = "overrides"

// Nothing stored, or something unreadable, runs on the configured bindings:
// the overrides refine an otherwise runnable job, and refusing it over a
// malformed refinement would strand a document. Anything a browser could put
// there was already refused by the create hook.
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
	// A JSONField round-trips an empty column as "null", which unmarshals into
	// the zero value anyway.
	if err := json.Unmarshal(data, &out); err != nil {
		return config.Overrides{}
	}
	return out
}

// Unset rather than "{}" when there are none, so a job queued without a picker
// touched is byte-identical to every job created before overrides existed.
func setJobOverrides(job *core.Record, overrides config.Overrides) {
	if overrides.Empty() {
		return
	}
	job.Set(jobOverridesField, overrides)
}

// The published snapshot, with whatever the job overrides rebuilt on its own
// bindings. A job with no overrides builds nothing.
func (p *Processor) effectiveSnapshot(job *core.Record) (config.Snapshot, error) {
	overrides := parseJobOverrides(job)
	if overrides.Empty() {
		return p.snapshot(), nil
	}
	return p.rt.WithOverrides(p.app, overrides)
}

// Refuses a job whose overrides name a provider that does not exist or cannot
// do the work. It runs in the create hook rather than the reprocess handler
// because the collection is writable by its owner, so the reprocess form makes
// jobs straight through the collection API: this is where both paths cross.
func validateJobOverrides(app core.App, cfg config.Config, job *core.Record) error {
	overrides := parseJobOverrides(job)
	if overrides.Empty() {
		return nil
	}
	// Chat and search are not refused here, only unused: the reprocess endpoint
	// is the layer that knows the request was a reprocess, so it tells the
	// client about an inapplicable binding.
	return overrides.Validate(app, cfg)
}
