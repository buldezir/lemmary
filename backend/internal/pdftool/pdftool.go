// Package pdftool does the app's PDF inspection, rendering and page extraction
// through PDFium (github.com/klippa-app/go-pdfium).
//
// PDFium runs in worker processes, not in the server: it parses untrusted
// uploads, and a crash, a runaway allocation or a hang there has to cost one
// worker rather than the server. A worker is this same binary re-executed (see
// init), started per call and gone when the call returns, so an idle server
// holds no PDFium memory. A call that outlives its timeout has its worker
// killed, which is what bounds a document PDFium never finishes with.
package pdftool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/multi_threaded"
	"github.com/klippa-app/go-pdfium/multi_threaded/worker"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
)

const (
	// InfoTimeout bounds opening a document to count its pages.
	InfoTimeout = 15 * time.Second
	// RenderTimeout bounds a render. Rendering every page of a long scan is the
	// slowest thing here, so it gets the largest budget.
	RenderTimeout = 5 * time.Minute
	// TextTimeout bounds one page's text extraction.
	TextTimeout = 30 * time.Second
	// AllTextTimeout bounds extracting the text of a whole document.
	AllTextTimeout = 2 * time.Minute
	// ExtractTimeout bounds copying one page range into a new file.
	ExtractTimeout = 60 * time.Second
	// MergeTimeout bounds one concatenation. It copies page objects without
	// re-rendering them, so it stays fast even over a long document.
	MergeTimeout = 60 * time.Second
)

var (
	// ErrNotPDF is returned for a path that does not carry a .pdf extension.
	ErrNotPDF = fmt.Errorf("pdftool: not a PDF file")
	// ErrPasswordRequired is returned by every call on a PDF that needs a user
	// password to open. One with only an owner password opens as any other.
	ErrPasswordRequired = errors.New("pdftool: the PDF needs a password to open")
	// ErrIncorrectPassword is returned by Decrypt for a password that does not
	// open the file.
	ErrIncorrectPassword = errors.New("pdftool: incorrect password")
	// ErrUnreadable wraps PDFium's own verdict that a file is not a PDF it can
	// read: empty, damaged, or not a PDF at all. Every other failure (a worker
	// that did not start or was killed) says nothing about the file.
	ErrUnreadable = errors.New("pdftool: not a readable PDF")
)

// documentErrors are the open failures that describe the file itself.
var documentErrors = []error{pdfiumerrors.ErrUnknown, pdfiumerrors.ErrFile, pdfiumerrors.ErrFormat, pdfiumerrors.ErrSecurity}

// pdfiumSaid reports whether err carries target. The worker's errors arrive
// over gRPC as plain text, so errors.Is cannot recognize go-pdfium's sentinels;
// their messages are the stable part, matched anywhere in err so a wrapped one
// still counts.
func pdfiumSaid(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), target.Error())
}

// workerArg is the argument a worker process is started with.
const workerArg = "__lemmary_pdfium_worker"

var pool pdfium.Pool

// init turns this process into a PDFium worker when the pool started it as one,
// and otherwise sets the pool up. Package init is the one place that runs in
// every binary linking pdftool -- the server, its CLI commands and each test
// binary -- before main or TestMain, which is what lets the pool re-execute
// whichever of those is running without each one wiring a worker entry point.
func init() {
	// go-plugin sets BASIC_PLUGIN=hello (go-pdfium's handshake cookie) on the
	// processes it starts; the argument alone could come from a user.
	if len(os.Args) > 1 && os.Args[1] == workerArg && os.Getenv("BASIC_PLUGIN") == "hello" {
		go exitWithParent(os.Getppid())
		worker.StartWorker(nil)
		os.Exit(0)
	}

	executable, err := os.Executable()
	if err != nil {
		executable = os.Args[0]
	}
	pool = withQuietStdout(func() pdfium.Pool {
		return multi_threaded.Init(multi_threaded.Config{
			MinIdle:  0,
			MaxIdle:  0,
			MaxTotal: max(1, min(4, runtime.GOMAXPROCS(0))),
			Command: multi_threaded.Command{
				BinPath:      executable,
				Args:         []string{workerArg},
				StartTimeout: InfoTimeout,
			},
		})
	})
}

// exitWithParent ends a worker whose parent is gone. go-plugin does not, and a
// server killed mid-call would otherwise leave its worker running with
// whatever document it held in memory.
func exitWithParent(parent int) {
	for range time.Tick(time.Second) {
		if os.Getppid() != parent {
			os.Exit(1)
		}
	}
}

// withQuietStdout runs build with os.Stdout pointed at a pipe that forwards
// only warnings and errors to slog.
//
// HACK: multi_threaded.Init creates a debug-level hclog logger on os.Stdout
// with no option to change it, and it logs every worker start and exit -- a
// few lines per PDF call with workers started per call. The logger keeps the
// *os.File it was given, so swapping os.Stdout for the duration of Init
// redirects it for good. Safe only because this runs during package init,
// before main starts any goroutine that might print.
func withQuietStdout(build func() pdfium.Pool) pdfium.Pool {
	reader, writer, err := os.Pipe()
	if err != nil {
		return build()
	}
	go forwardWorkerLog(reader)
	stdout := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = stdout }()
	return build()
}

// forwardWorkerLog passes on warnings and errors, and whatever a worker itself
// printed: go-plugin relays a worker's stderr under the logger name
// "plugin.<binary>" at debug level, and that is where a worker that dies at
// start says why. The lines a worker prints on every normal exit are not.
func forwardWorkerLog(reader *os.File) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		fromWorker := strings.Contains(line, "] plugin.") &&
			!strings.Contains(line, "plugin address:") &&
			!strings.Contains(line, "plugin server: accept") &&
			!strings.Contains(line, "stream copy")
		if fromWorker || strings.Contains(line, "[ERROR]") || strings.Contains(line, "[WARN]") {
			slog.Default().Warn("pdfium worker", "component", "pdftool", "log", line)
		}
	}
}

// withInstance runs fn on a worker of its own and returns its error. When ctx
// ends or timeout passes first, the worker is killed, which interrupts PDFium
// mid-call, and the context error is returned.
func withInstance(ctx context.Context, timeout time.Duration, fn func(pdfium.Pdfium) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	instance, err := pool.GetInstanceWithContext(ctx)
	if err != nil {
		return fmt.Errorf("pdftool: start pdfium worker: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- fn(instance) }()
	select {
	case err := <-done:
		instance.Close()
		return err
	case <-ctx.Done():
		if err := instance.Kill(); err != nil {
			slog.Default().Warn("pdfium worker did not stop cleanly", "component", "pdftool", "error", err)
		}
		<-done
		return fmt.Errorf("pdftool: pdfium worker stopped: %w", ctx.Err())
	}
}

// RequirePDF rejects a path that is not a PDF before a worker is started.
func RequirePDF(pdfPath string) error {
	if strings.ToLower(filepath.Ext(pdfPath)) != ".pdf" {
		return ErrNotPDF
	}
	return nil
}

func openDocument(instance pdfium.Pdfium, pdfPath, password string) (references.FPDF_DOCUMENT, error) {
	info, err := os.Stat(pdfPath)
	if err != nil {
		return "", fmt.Errorf("pdftool: %w", err)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("%w: the file is empty", ErrUnreadable)
	}
	request := &requests.OpenDocument{FilePath: &pdfPath}
	if password != "" {
		request.Password = &password
	}
	doc, err := instance.OpenDocument(request)
	if err == nil {
		return doc.Document, nil
	}
	if pdfiumSaid(err, pdfiumerrors.ErrPassword) {
		if password != "" {
			return "", ErrIncorrectPassword
		}
		return "", ErrPasswordRequired
	}
	for _, documentErr := range documentErrors {
		if pdfiumSaid(err, documentErr) {
			return "", fmt.Errorf("%w: %s: %w", ErrUnreadable, filepath.Base(pdfPath), err)
		}
	}
	return "", fmt.Errorf("pdftool: open %s: %w", filepath.Base(pdfPath), err)
}

// closeDocument releases a document handle. A failure is not worth reporting:
// the worker exits at the end of the call and takes every handle with it.
func closeDocument(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT) {
	_, _ = instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc})
}

// withDocument opens pdfPath on a worker and runs fn on it.
func withDocument(ctx context.Context, timeout time.Duration, pdfPath string, fn func(pdfium.Pdfium, references.FPDF_DOCUMENT) error) error {
	return withInstance(ctx, timeout, func(instance pdfium.Pdfium) error {
		doc, err := openDocument(instance, pdfPath, "")
		if err != nil {
			return err
		}
		defer closeDocument(instance, doc)
		return fn(instance, doc)
	})
}

func countPages(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT) (int, error) {
	result, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc})
	if err != nil {
		return 0, fmt.Errorf("pdftool: page count: %w", err)
	}
	if result.PageCount <= 0 {
		return 0, fmt.Errorf("%w: document reports %d pages", ErrUnreadable, result.PageCount)
	}
	return result.PageCount, nil
}

// PageCount reports how many pages pdfPath holds. A failure here also means the
// file is not a PDF PDFium can read, so callers use it as a validity check.
func PageCount(ctx context.Context, pdfPath string) (int, error) {
	var count int
	err := withDocument(ctx, InfoTimeout, pdfPath, func(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT) error {
		var err error
		count, err = countPages(instance, doc)
		return err
	})
	return count, err
}

// renderPNG renders one page so its longest edge is maxEdge pixels. The worker
// encodes and writes the PNG itself, so the bitmap never crosses into the
// server. BestSpeed because a page image is read once, by a model or a browser:
// encoding time, not file size, is what an OCR run of a long scan waits on.
func renderPNG(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT, index, maxEdge int, outPath string) error {
	_, err := instance.RenderToFile(&requests.RenderToFile{
		RenderPageInPixels: &requests.RenderPageInPixels{
			Page:        requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: index}},
			Width:       maxEdge,
			Height:      maxEdge,
			RenderFlags: enums.FPDF_RENDER_FLAG_ANNOT,
		},
		OutputFormat:        requests.RenderToFileOutputFormatPNG,
		OutputTarget:        requests.RenderToFileOutputTargetFile,
		TargetFilePath:      outPath,
		PNGCompressionLevel: png.BestSpeed,
	})
	if err != nil {
		return fmt.Errorf("pdftool: render page %d: %w", index+1, err)
	}
	return nil
}

// RenderPages renders every page of pdfPath into outDir as page-<n>.png, scaled
// so the longest edge is maxEdge pixels, and returns the paths in page order.
func RenderPages(ctx context.Context, pdfPath, outDir string, maxEdge, pageCount int) ([]string, error) {
	if err := RequirePDF(pdfPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, fmt.Errorf("pdftool: prepare render dir: %w", err)
	}

	paths := make([]string, 0, pageCount)
	err := withDocument(ctx, RenderTimeout, pdfPath, func(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT) error {
		count, err := countPages(instance, doc)
		if err != nil {
			return err
		}
		if count != pageCount {
			return fmt.Errorf("pdftool: document has %d pages, expected %d", count, pageCount)
		}
		for i := range count {
			path := PagePNGPath(outDir, i+1)
			if err := renderPNG(instance, doc, i, maxEdge, path); err != nil {
				return err
			}
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		for _, path := range paths {
			os.Remove(path)
		}
		return nil, err
	}
	return paths, nil
}

// RenderPage renders a single page of pdfPath to outPath (a .png path), scaled
// so the longest edge is maxEdge pixels.
func RenderPage(ctx context.Context, pdfPath, outPath string, maxEdge, page int) error {
	if err := RequirePDF(pdfPath); err != nil {
		return err
	}
	if page < 1 {
		return fmt.Errorf("pdftool: invalid page %d", page)
	}
	return withDocument(ctx, RenderTimeout, pdfPath, func(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT) error {
		return renderPNG(instance, doc, page-1, maxEdge, outPath)
	})
}

// PagePNGPath is where RenderPages puts the PNG for a 1-based page number.
func PagePNGPath(outDir string, page int) string {
	return filepath.Join(outDir, fmt.Sprintf("page-%d.png", page))
}

// pageText returns one page's text layer in content order, so a two-column page
// reads column by column. It is "" for a scanned page.
func pageText(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT, index int) (string, error) {
	page, err := instance.FPDF_LoadPage(&requests.FPDF_LoadPage{Document: doc, Index: index})
	if err != nil {
		return "", fmt.Errorf("pdftool: load page %d: %w", index+1, err)
	}
	defer func() { _, _ = instance.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: page.Page}) }()

	textPage, err := instance.FPDFText_LoadPage(&requests.FPDFText_LoadPage{Page: requests.Page{ByReference: &page.Page}})
	if err != nil {
		return "", fmt.Errorf("pdftool: load text of page %d: %w", index+1, err)
	}
	defer func() { _, _ = instance.FPDFText_ClosePage(&requests.FPDFText_ClosePage{TextPage: textPage.TextPage}) }()

	chars, err := instance.FPDFText_CountChars(&requests.FPDFText_CountChars{TextPage: textPage.TextPage})
	if err != nil {
		return "", fmt.Errorf("pdftool: count text of page %d: %w", index+1, err)
	}
	if chars.Count <= 0 {
		return "", nil
	}
	text, err := instance.FPDFText_GetText(&requests.FPDFText_GetText{TextPage: textPage.TextPage, Count: chars.Count})
	if err != nil {
		return "", fmt.Errorf("pdftool: read text of page %d: %w", index+1, err)
	}
	return strings.TrimSpace(text.Text), nil
}

// PageText extracts the text layer of one page. It returns "" for a scanned
// page, which is how callers tell born-digital pages from images.
func PageText(ctx context.Context, pdfPath string, page int) (string, error) {
	if err := RequirePDF(pdfPath); err != nil {
		return "", err
	}
	if page < 1 {
		return "", fmt.Errorf("pdftool: invalid page %d", page)
	}
	var text string
	err := withDocument(ctx, TextTimeout, pdfPath, func(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT) error {
		var err error
		text, err = pageText(instance, doc, page-1)
		return err
	})
	return text, err
}

// AllPagesText extracts the text layer of every page with one document open and
// returns exactly pageCount entries, one per page in page order.
//
// A file with fewer pages than pageCount leaves the missing entries empty
// rather than failing: callers index by page number.
func AllPagesText(ctx context.Context, pdfPath string, pageCount int) ([]string, error) {
	if err := RequirePDF(pdfPath); err != nil {
		return nil, err
	}
	if pageCount < 1 {
		return nil, fmt.Errorf("pdftool: invalid page count %d", pageCount)
	}
	out := make([]string, pageCount)
	err := withDocument(ctx, AllTextTimeout, pdfPath, func(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT) error {
		count, err := countPages(instance, doc)
		if err != nil {
			return err
		}
		for i := range min(count, pageCount) {
			if out[i], err = pageText(instance, doc, i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ExtractRange writes pages from..to of pdfPath to outPath as a new PDF.
//
// The page objects are copied, not re-rasterized, so the text layer and image
// quality survive.
func ExtractRange(ctx context.Context, pdfPath string, from, to int, outPath string) error {
	if err := RequirePDF(pdfPath); err != nil {
		return err
	}
	if from < 1 || to < from {
		return fmt.Errorf("pdftool: invalid page range %d-%d", from, to)
	}
	pageRange := fmt.Sprintf("%d-%d", from, to)
	err := withInstance(ctx, ExtractTimeout, func(instance pdfium.Pdfium) error {
		return writeNewDocument(instance, outPath, []pageSource{{path: pdfPath, pageRange: &pageRange}})
	})
	if err != nil {
		return err
	}
	return canonicalizeFileID(ctx, outPath, true)
}

// Merge concatenates inputs into a single PDF at outPath.
//
// outPath may name one of the inputs: the result is written beside it and
// moved into place, so a failed run also leaves the old outPath untouched.
func Merge(ctx context.Context, outPath string, inputs ...string) error {
	if len(inputs) < 2 {
		return fmt.Errorf("pdftool: merge needs at least two inputs, got %d", len(inputs))
	}
	for _, input := range inputs {
		if err := RequirePDF(input); err != nil {
			return err
		}
	}
	if err := RequirePDF(outPath); err != nil {
		return err
	}
	sources := make([]pageSource, len(inputs))
	for i, input := range inputs {
		sources[i] = pageSource{path: input}
	}
	err := withInstance(ctx, MergeTimeout, func(instance pdfium.Pdfium) error {
		return writeNewDocument(instance, outPath, sources)
	})
	if err != nil {
		return err
	}
	return canonicalizeFileID(ctx, outPath, true)
}

// pageSource is a file and the pages to take from it; a nil pageRange takes
// them all.
type pageSource struct {
	path      string
	pageRange *string
}

// writeNewDocument builds a new document from sources in order and saves it to
// outPath through a temporary file next to it.
func writeNewDocument(instance pdfium.Pdfium, outPath string, sources []pageSource) error {
	created, err := instance.FPDF_CreateNewDocument(&requests.FPDF_CreateNewDocument{})
	if err != nil {
		return fmt.Errorf("pdftool: new document: %w", err)
	}
	defer closeDocument(instance, created.Document)

	next := 0
	for _, source := range sources {
		doc, err := openDocument(instance, source.path, "")
		if err != nil {
			return err
		}
		_, err = instance.FPDF_ImportPages(&requests.FPDF_ImportPages{
			Source:      doc,
			Destination: created.Document,
			PageRange:   source.pageRange,
			Index:       next,
		})
		closeDocument(instance, doc)
		if err != nil {
			return fmt.Errorf("pdftool: copy pages from %s: %w", filepath.Base(source.path), err)
		}
		count, err := countPages(instance, created.Document)
		if err != nil {
			return err
		}
		next = count
	}

	return saveAs(instance, created.Document, outPath, requests.SaveFlagNoIncremental)
}

// saveAs writes doc to outPath through a temporary file next to it, so a
// failed save leaves whatever was at outPath untouched.
func saveAs(instance pdfium.Pdfium, doc references.FPDF_DOCUMENT, outPath string, flags requests.SaveFlags) error {
	tmpPath := outPath + ".tmp"
	if _, err := instance.FPDF_SaveAsCopy(&requests.FPDF_SaveAsCopy{
		Document: doc,
		FilePath: &tmpPath,
		Flags:    flags,
	}); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("pdftool: save: %w", err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("pdftool: move saved document: %w", err)
	}
	return nil
}
