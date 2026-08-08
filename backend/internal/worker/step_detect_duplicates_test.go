package worker

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/duplicates"
)

func TestDetectDuplicatesShouldSkipWithoutOCR(t *testing.T) {
	docs := coreTestDocumentsCollection()
	doc := core.NewRecord(docs)

	step := &DetectDuplicatesStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("expected skip without ocr text")
	}
}

func TestDetectDuplicatesShouldNotSkipWithOCR(t *testing.T) {
	docs := coreTestDocumentsCollection()
	doc := core.NewRecord(docs)
	doc.Set("ocr_text", "enough text to process")

	step := &DetectDuplicatesStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatal(err)
	}
	if skipped {
		t.Fatal("expected not to skip when ocr text exists")
	}
	if state.OCRText != "enough text to process" {
		t.Fatalf("OCRText=%q", state.OCRText)
	}
}

func TestExtractMetadataShouldSkipWhenDuplicateOfSet(t *testing.T) {
	docs := core.NewBaseCollection("documents")
	docs.Fields.Add(&core.TextField{Name: "duplicate_of"})
	doc := core.NewRecord(docs)
	doc.Set("duplicate_of", "originaldoc0001")

	step := &ExtractMetadataStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("expected skip when duplicate_of is set")
	}
}

func TestApplyMetadataShouldSkipWhenDuplicateOfSet(t *testing.T) {
	docs := core.NewBaseCollection("documents")
	docs.Fields.Add(&core.TextField{Name: "duplicate_of"})
	doc := core.NewRecord(docs)
	doc.Set("duplicate_of", "originaldoc0001")

	step := &ApplyMetadataStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("expected skip when duplicate_of is set")
	}
}

func TestFingerprintHelpersUsedByDetectStep(t *testing.T) {
	text := strings.Repeat("sample document text for fingerprinting purposes ", 4)
	fp := duplicates.FingerprintHex(text)
	if fp == "" {
		t.Fatal("expected fingerprint")
	}
}
