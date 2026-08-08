package duplicates

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestSHA256ReaderStable(t *testing.T) {
	sum1, err := SHA256Reader(strings.NewReader("hello duplicate"))
	if err != nil {
		t.Fatal(err)
	}
	sum2, err := SHA256Reader(strings.NewReader("hello duplicate"))
	if err != nil {
		t.Fatal(err)
	}
	if sum1 != sum2 || len(sum1) != 64 {
		t.Fatalf("unexpected checksum %q / %q", sum1, sum2)
	}
	other, err := SHA256Reader(strings.NewReader("hello different"))
	if err != nil {
		t.Fatal(err)
	}
	if other == sum1 {
		t.Fatal("expected different checksum")
	}
}

func TestNormalizeAndJaccard(t *testing.T) {
	a := NormalizeText("Invoice #123 — Acme Plumbing!!  Total: $40.")
	b := NormalizeText("invoice 123 acme plumbing total 40")
	if a == "" || !strings.Contains(a, "acme") {
		t.Fatalf("normalize failed: %q", a)
	}
	score := JaccardSimilarity(WordShingles(a, 3), WordShingles(b, 3))
	if score < 0.5 {
		t.Fatalf("expected high-ish jaccard, got %v (%q vs %q)", score, a, b)
	}
}

func TestTextSimilarityNearDuplicate(t *testing.T) {
	base := strings.Repeat("the quick brown fox jumps over the lazy dog ", 5)
	a := base + "invoice total forty dollars acme plumbing"
	b := base + "invoice total forty dollars acme plumbing."
	if !IsNearDuplicate(a, b, 0.9) {
		t.Fatalf("expected near duplicate, score=%v", TextSimilarity(a, b))
	}
	c := strings.Repeat("completely unrelated medical lab results for patient zero ", 5)
	if IsNearDuplicate(a, c, 0.9) {
		t.Fatal("expected unrelated texts not to match")
	}
}

func TestFingerprintRoundTrip(t *testing.T) {
	text := strings.Repeat("sample document text for fingerprinting purposes ", 4)
	fp := FingerprintHex(text)
	if len(fp) != 16 {
		t.Fatalf("fingerprint len=%d value=%q", len(fp), fp)
	}
	v, ok := ParseFingerprintHex(fp)
	if !ok || v == 0 {
		t.Fatalf("parse failed: %q", fp)
	}
	if HammingDistance(v, v) != 0 {
		t.Fatal("hamming self distance")
	}
}

func TestShortTextNoFingerprint(t *testing.T) {
	if fp := FingerprintHex("short"); fp != "" {
		t.Fatalf("expected empty fingerprint, got %q", fp)
	}
}

func TestEligibleNearDuplicateOriginal(t *testing.T) {
	docs := core.NewBaseCollection("documents")
	docs.Fields.Add(&core.TextField{Name: "created"})
	docs.Fields.Add(&core.TextField{Name: "duplicate_of"})

	older := core.NewRecord(docs)
	older.Set("created", "2026-01-01 10:00:00.000Z")
	newer := core.NewRecord(docs)
	newer.Set("created", "2026-01-02 10:00:00.000Z")

	if !EligibleNearDuplicateOriginal(newer, older) {
		t.Fatal("expected older candidate to be eligible original")
	}
	if EligibleNearDuplicateOriginal(older, newer) {
		t.Fatal("newer candidate must not be treated as original for older document")
	}

	dup := core.NewRecord(docs)
	dup.Set("created", "2026-01-01 09:00:00.000Z")
	dup.Set("duplicate_of", "someoriginal0001")
	if EligibleNearDuplicateOriginal(newer, dup) {
		t.Fatal("already-marked duplicate must not be an original")
	}
}

func TestIsChecksumUniqueViolation(t *testing.T) {
	err := errors.New("UNIQUE constraint failed: index 'idx_documents_user_checksum'")
	if !IsChecksumUniqueViolation(err) {
		t.Fatal("expected unique checksum violation")
	}
	if IsChecksumUniqueViolation(errors.New("something else")) {
		t.Fatal("expected non-match")
	}
	if IsChecksumUniqueViolation(fmt.Errorf("wrap: %w", err)) {
		// wrapped should still match via Unwrap
	} else {
		t.Fatal("expected wrapped unique checksum violation")
	}
}
