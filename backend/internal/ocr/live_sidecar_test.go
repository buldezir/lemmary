package ocr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/pdftool"
	"lemmary/backend/internal/pdftool/testpdf"
)

// These tests talk to a real OCR sidecar, and are skipped unless one is named.
//
// The unit tests beside them assert the wire contract against a stub built from
// the upstream schemas, which is what CI can afford: the images are several
// gigabytes and take minutes to warm up. That leaves one thing unproven -- that
// the schema we built the stub from is the one the container actually speaks --
// and this is where that gets checked, by hand, against a container:
//
//	docker compose -f docker-compose.yml -f docker-compose.local-ocr.yml \
//	  --profile docling up -d
//	LEMMARY_DOCLING_URL=http://127.0.0.1:5001 \
//	  go test -tags vectors ./internal/ocr/ -run Live -v
const (
	envDoclingURL     = "LEMMARY_DOCLING_URL"
	envDoclingAuthURL = "LEMMARY_DOCLING_AUTH_URL"
	envDoclingAuthKey = "LEMMARY_DOCLING_AUTH_KEY"
)

// liveTimeout is generous on purpose. A local engine reads a page in seconds to
// tens of seconds, and the first request after a container start also loads the
// models -- the same reason docs/local_ocr.md tells operators to raise
// OCR_TIMEOUT_SEC well past its 40 second default.
const liveTimeout = 5 * time.Minute

func liveURL(t *testing.T, key string) string {
	t.Helper()
	url := strings.TrimSpace(os.Getenv(key))
	if url == "" {
		t.Skipf("%s is not set; skipping the live sidecar test", key)
	}
	return url
}

// livePDF is a born-digital PDF carrying two phrases worth looking for.
func livePDF(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "invoice.pdf")
	data := testpdf.Multipage(1, "Invoice INV-1001", "Acme Plumbing GmbH")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

// liveScan renders that PDF to a PNG, which is the case that actually exercises
// OCR: a page of pixels with no text layer to fall back on.
func liveScan(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "invoice.pdf")
	if err := os.WriteFile(pdfPath, testpdf.Multipage(1, "Invoice INV-1001", "Acme Plumbing GmbH"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	pngPath := filepath.Join(dir, "scan.png")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := pdftool.RenderPage(ctx, pdfPath, pngPath, 2000, 1); err != nil {
		t.Skipf("cannot rasterize a test scan (poppler missing?): %v", err)
	}
	return pngPath
}

func assertReadsInvoice(t *testing.T, provider Provider, path, mimeType string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	start := time.Now()
	text, err := provider.ExtractText(ctx, path, mimeType)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	t.Logf("%s read %s in %s, %d chars:\n%s",
		provider.Name(), filepath.Base(path), time.Since(start).Round(time.Millisecond), len(text), text)

	// Loose on purpose. OCR is allowed to disagree about spacing and case; what
	// must survive is that the words on the page came back in something we can
	// index. A stricter assertion would fail on a fine result.
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, want := range []string{"inv-1001", "acme"} {
		if !strings.Contains(normalized, want) {
			t.Errorf("extracted text does not contain %q", want)
		}
	}
}

func TestLiveDoclingReadsABornDigitalPDF(t *testing.T) {
	provider := NewDoclingProvider(liveURL(t, envDoclingURL), "", "", liveTimeout, nil)
	assertReadsInvoice(t, provider, livePDF(t), "application/pdf")
}

// The one that proves OCR ran, rather than a PDF text layer being read.
func TestLiveDoclingReadsAScan(t *testing.T) {
	provider := NewDoclingProvider(liveURL(t, envDoclingURL), "", "", liveTimeout, nil)
	assertReadsInvoice(t, provider, liveScan(t), "image/png")
}

// Docling reads every format Lemmary accepts, so this is about what it refuses
// rather than what it lacks: an archive or another binary must fail here, by
// name, instead of reaching the sidecar and coming back as an opaque 500.
func TestLiveDoclingRejectsWhatItCannotRead(t *testing.T) {
	provider := NewDoclingProvider(liveURL(t, envDoclingURL), "", "", liveTimeout, nil)

	path := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(path, []byte("PK\x03\x04"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := provider.ExtractText(context.Background(), path, ""); err == nil {
		t.Fatal("want an error for an unsupported mime type")
	}
}

// The engine binding is an optional free-text field, not an enum, and docling
// answers 200 for a name it does not recognise -- it falls back to its default
// and says nothing. So this can only assert that binding one is harmless; a
// typo there is invisible by design, which is why docs/local_ocr.md lists the
// accepted values rather than leaving an operator to guess.
func TestLiveDoclingAcceptsABoundEngine(t *testing.T) {
	url := liveURL(t, envDoclingURL)
	for _, engine := range []string{"", "rapidocr"} {
		t.Run("engine="+engine, func(t *testing.T) {
			provider := NewDoclingProvider(url, engine, "", liveTimeout, nil)
			assertReadsInvoice(t, provider, liveScan(t), "image/png")
		})
	}
}

// The optional credential, against a container actually started with
// DOCLING_SERVE_API_KEY -- which is the only way to find out whether the header
// we send is the header it wants.
//
//	docker run -e DOCLING_SERVE_API_KEY=s3cret ... -p 5002:5001 <image>
//	LEMMARY_DOCLING_AUTH_URL=http://127.0.0.1:5002 //	  LEMMARY_DOCLING_AUTH_KEY=s3cret go test ... -run Live
func TestLiveDoclingAuthenticates(t *testing.T) {
	url := liveURL(t, envDoclingAuthURL)
	key := strings.TrimSpace(os.Getenv(envDoclingAuthKey))
	if key == "" {
		t.Skipf("%s is not set", envDoclingAuthKey)
	}

	t.Run("with the key", func(t *testing.T) {
		provider := NewDoclingProvider(url, "", key, liveTimeout, nil)
		assertReadsInvoice(t, provider, liveScan(t), "image/png")
	})

	// The half that matters: a provider row whose key was never filled in must
	// fail loudly against a guarded sidecar rather than quietly returning
	// nothing, because OCR_API_KEY is optional for this SDK and an operator who
	// guarded the container can forget it.
	t.Run("without the key", func(t *testing.T) {
		provider := NewDoclingProvider(url, "", "", liveTimeout, nil)
		ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
		defer cancel()
		if _, err := provider.ExtractText(ctx, liveScan(t), "image/png"); err == nil {
			t.Fatal("want an error from a guarded sidecar with no key")
		} else {
			t.Logf("refused as expected: %v", err)
		}
	})
}
