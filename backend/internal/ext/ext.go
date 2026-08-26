// Package ext is the seam a private edition of this binary adds itself through.
//
// The open-source build supplies the zero Edition, which adds nothing: every
// field below is empty, and appwire wires exactly what it wired before this
// package existed. A private build supplies a populated one from a file guarded
// by its own build tag, so the two never appear in the same compilation and a
// fork carrying the private edition adds files rather than editing shared ones.
// That is the whole point of the arrangement — a fork that only adds files has
// no merge conflicts to resolve when it pulls from upstream.
//
// Why a value rather than an init()-time registry: a registry is mutable global
// state, which this codebase avoids everywhere else, and it makes the set of
// additions depend on which packages happen to be linked in — a property that
// is invisible at the call site and impossible to assert on in a test. An
// Edition is built in one place, can be logged, and can be handed to a test
// verbatim.
package ext

import (
	"github.com/pocketbase/pocketbase"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/worker"
)

// Deps are the long-lived objects appwire has already constructed by the time
// an edition runs. They are passed rather than rebuilt because both are
// process-global and stateful: a second fulltext.Index would open a second
// Bleve writer on the same directory, and a second config.Runtime would serve
// settings that no reload ever refreshes.
type Deps struct {
	Runtime  *config.Runtime
	FullText *fulltext.Index
}

// Edition is the set of additions one build of this binary makes to the core
// feature set. The zero value is the open-source edition.
type Edition struct {
	// Name appears in the startup log line so a running container can be
	// identified from its logs alone. Empty means the open-source build.
	Name string

	// Steps are appended to the worker's pipeline step registry. A step whose
	// Name() matches a built-in replaces it, which is how an edition changes
	// the behaviour of an existing stage rather than only adding a new one.
	Steps []worker.StepFactory

	// StepPlans rewrite the default step list for jobs created from a newly
	// uploaded document, in order. They do not affect jobs whose steps the
	// caller chose explicitly via worker.SetCreateSteps.
	StepPlans []worker.StepPlan

	// DecorateSnapshot runs on every settings reload, after the core clients
	// are built and before the snapshot is published, and may replace any of
	// them.
	//
	// One hook covers OCR, extraction, chat, Deep Search and splitting because
	// they are all just fields of the snapshot; a per-client hook would be five
	// fields that have to be kept in step with config.Snapshot forever.
	DecorateSnapshot config.SnapshotDecorator

	// Register runs last, once, with the live app. Routes, hooks, cron jobs and
	// CLI subcommands all attach here, the same way every core feature package
	// attaches through its own Register.
	Register []func(app *pocketbase.PocketBase, deps Deps)
}
