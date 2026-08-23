package pdfsplit

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidatePartsAcceptsAnExactCover(t *testing.T) {
	t.Parallel()

	cases := [][]Part{
		{{From: 1, To: 4}},
		{{From: 1, To: 1}, {From: 2, To: 4}},
		{{From: 1, To: 1}, {From: 2, To: 2}, {From: 3, To: 3}, {From: 4, To: 4}},
	}
	for _, parts := range cases {
		if err := ValidateParts(parts, 4); err != nil {
			t.Fatalf("ValidateParts(%+v) error: %v", parts, err)
		}
	}
}

func TestValidatePartsRejectsAnythingButACover(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		parts []Part
		want  string
	}{
		{"empty", nil, "at least one part"},
		{"more parts than pages", []Part{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}}, "cannot fit"},
		{"gap", []Part{{From: 1, To: 1}, {From: 3, To: 4}}, "expected page 2"},
		{"overlap", []Part{{From: 1, To: 3}, {From: 3, To: 4}}, "expected page 4"},
		{"unsorted", []Part{{From: 2, To: 4}, {From: 1, To: 1}}, "expected page 1"},
		{"does not start at page 1", []Part{{From: 2, To: 4}}, "expected page 1"},
		{"stops short of the last page", []Part{{From: 1, To: 3}}, "expected page 4"},
		{"runs past the last page", []Part{{From: 1, To: 5}}, "invalid page range"},
		{"reversed range", []Part{{From: 3, To: 2}}, "invalid page range"},
		{"zero page", []Part{{From: 0, To: 4}}, "invalid page range"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateParts(tc.parts, 4)
			if err == nil {
				t.Fatalf("expected an error for %+v", tc.parts)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestStartRestoresTheUploadWhenThePartsAreInvalid(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	stageDir(t, root, "upload-1", "owner-a", 4, time.Now().UTC().Add(time.Hour))

	// nil app: the request must be rejected before anything touches the DB.
	_, err := Start(nil, "owner-a", "upload-1", []Part{{From: 1, To: 2}})
	if err == nil {
		t.Fatal("expected an error for parts that do not cover every page")
	}
	if _, _, ok := Lookup("upload-1", "owner-a"); !ok {
		t.Fatal("a rejected split must leave the upload available for a fixed request")
	}
}

func TestStartRejectsAnUnknownUpload(t *testing.T) {
	resetStaging(t)

	if _, err := Start(nil, "owner-a", "missing", []Part{{From: 1, To: 1}}); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("err=%v want ErrUploadNotFound", err)
	}
}

func TestPartBaseName(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"scan-2024.pdf", "scan-2024"},
		{"Scan 2024 (kitchen).pdf", "Scan-2024-kitchen"},
		{"/tmp/uploads/statements.PDF", "statements"},
		{"", "document"},
		{"???.pdf", "document"},
		{"Rechnung_Müller.pdf", "Rechnung_M-ller"},
		{strings.Repeat("a", 200) + ".pdf", strings.Repeat("a", maxPartNameBytes)},
	}
	for _, tc := range cases {
		if got := partBaseName(tc.in); got != tc.want {
			t.Fatalf("partBaseName(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestPartFileName(t *testing.T) {
	t.Parallel()

	if got := partFileName("scan", Part{From: 3, To: 3}); got != "scan-page-3.pdf" {
		t.Fatalf("single page name=%q", got)
	}
	if got := partFileName("scan", Part{From: 2, To: 5}); got != "scan-pages-2-5.pdf" {
		t.Fatalf("range name=%q", got)
	}
}

func TestSplitRestoresTheUploadWhenNothingWasCreated(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	item := stageDir(t, root, "upload-1", "owner-a", 2, time.Now().UTC().Add(time.Hour))

	// nil app: the run fails before a single document exists, which is the case
	// that used to destroy the staged PDF and force a full re-upload.
	jobID, err := Start(nil, "owner-a", "upload-1", []Part{{From: 1, To: 2}})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	waitForSplitStatus(t, jobID, JobStatusFailed)

	if _, _, ok := Lookup("upload-1", "owner-a"); !ok {
		t.Fatal("a split that created nothing must leave the upload staged for a retry")
	}
	if _, err := os.Stat(sourcePathOf(item)); err != nil {
		t.Fatalf("the staged PDF was deleted by a failed split: %v", err)
	}
}

func waitForSplitStatus(t *testing.T, jobID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if job, ok := GetJob(jobID); ok && job.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach status %q", jobID, want)
}
