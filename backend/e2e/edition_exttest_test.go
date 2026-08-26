//go:build lemmary_exttest

package e2e

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"lemmary/backend/internal/appwire"
)

// These tests run only under the lemmary_exttest tag, against the throwaway
// edition in internal/appwire/edition_exttest.go. They exist so a change that
// stops wiring one of ext.Edition's fields fails here rather than in a private
// fork's CI weeks later.
//
//	go test -tags lemmary_exttest ./e2e/ -run Edition

func TestEditionRegisterRunsWithLiveDeps(t *testing.T) {
	h := StartShared(t)

	status, raw := h.doJSON(t, http.MethodGet, "/api/exttest/edition", h.userToken(t), nil)
	requireStatus(t, status, http.StatusOK, raw)

	var body struct {
		Edition string `json:"edition"`
		OCR     string `json:"ocr"`
		HasDeps bool   `json:"has_deps"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}

	if body.Edition != "exttest" {
		t.Fatalf("edition route answered for %q", body.Edition)
	}
	// A route the edition bound is proof Edition.Register ran; deps being
	// non-nil is proof it was handed the objects appwire had already built
	// rather than a zero value.
	if !body.HasDeps {
		t.Fatal("edition was registered without a runtime or a full-text index")
	}
	// The decorator wraps whatever the core built, so this name can only appear
	// if DecorateSnapshot was installed and ran on the bootstrap reload.
	if body.OCR != appwire.ExtTestOCRName {
		t.Fatalf("snapshot decorator did not reach the published snapshot: ocr=%q", body.OCR)
	}
}

func TestEditionStepPlanReachesCreatedJobs(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.png"))
	id := jsonGetString(rec, "id")

	jobs, err := h.App.FindRecordsByFilter(
		"processing_jobs",
		"document = {:document}",
		"-created",
		1,
		0,
		map[string]any{"document": id},
	)
	if err != nil {
		t.Fatalf("find processing job for %s: %v", id, err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected a processing job for the uploaded document")
	}

	steps := jobSteps(t, jobs[0])
	if !slices.Contains(steps, appwire.ExtTestStepName) {
		t.Fatalf("edition step plan did not reach the created job: steps=%v", steps)
	}
	// Prepended by the plan; asserting the position proves the plan rewrote the
	// list rather than something else appending a step by coincidence.
	if steps[0] != appwire.ExtTestStepName {
		t.Fatalf("expected %q first, got %v", appwire.ExtTestStepName, steps)
	}
}
