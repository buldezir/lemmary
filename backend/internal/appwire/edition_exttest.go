//go:build lemmary_exttest

package appwire

import (
	"context"
	"net/http"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/ext"
	"lemmary/backend/internal/ocr"
	"lemmary/backend/internal/worker"
)

// This file is the throwaway edition the seam tests build against, under the
// lemmary_exttest tag. It ships in the open-source repository on purpose: the
// private edition lives in a fork nobody here can build, so without a stand-in
// the only thing verifying this seam would be that fork's own CI, and the seam
// would rot upstream between merges with nothing failing.
//
// It exercises every field of ext.Edition. A field that stops being wired makes
// e2e/edition_exttest_test.go fail -- that assertion lives in the private
// development suite, present only when it is cloned into dev/, so a
// build tagged lemmary_exttest here still has to compile this file either way.

// ExtTestStepName is the step the edition contributes. Exported so the test can
// name it without agreeing a string literal twice.
const ExtTestStepName = "exttest_marker"

// ExtTestOCRName is what the decorated snapshot reports as its OCR provider.
const ExtTestOCRName = "exttest-ocr"

type extTestStep struct{}

func (extTestStep) Name() string                                 { return ExtTestStepName }
func (extTestStep) ShouldSkip(*worker.StepState) (bool, error)   { return false, nil }
func (extTestStep) Run(context.Context, *worker.StepState) error { return nil }

type extTestOCR struct{ inner ocr.Provider }

func (p extTestOCR) Name() string { return ExtTestOCRName }
func (p extTestOCR) ExtractText(ctx context.Context, filePath, mimeType string) (string, error) {
	if p.inner == nil {
		return "", nil
	}
	return p.inner.ExtractText(ctx, filePath, mimeType)
}

func edition() ext.Edition {
	return ext.Edition{
		Name: "exttest",

		Steps: []worker.StepFactory{
			func(ocr.Provider, ai.Extractor) worker.Step { return extTestStep{} },
		},

		// Prepended rather than appended so the assertion cannot pass by
		// accident on a list that merely ends with the right name.
		StepPlans: []worker.StepPlan{
			func(steps []string) []string {
				return append([]string{ExtTestStepName}, steps...)
			},
		},

		DecorateSnapshot: func(snap config.Snapshot) config.Snapshot {
			snap.OCR = extTestOCR{inner: snap.OCR}
			return snap
		},

		Register: []func(*pocketbase.PocketBase, ext.Deps){
			func(app *pocketbase.PocketBase, deps ext.Deps) {
				app.OnServe().BindFunc(func(e *core.ServeEvent) error {
					e.Router.GET("/api/exttest/edition", func(re *core.RequestEvent) error {
						ocrName := ""
						if provider := deps.Runtime.Snapshot().OCR; provider != nil {
							ocrName = provider.Name()
						}
						return re.JSON(http.StatusOK, map[string]any{
							"edition":    "exttest",
							"ocr":        ocrName,
							"has_deps":   deps.Runtime != nil && deps.FullText != nil,
							"step_name":  ExtTestStepName,
							"registered": true,
						})
					})
					return e.Next()
				})
			},
		},
	}
}
