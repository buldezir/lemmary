// Package pdftool wraps the poppler-utils command line tools the app relies on
// for PDF inspection, rendering and page extraction.
//
// poppler-utils is the only native runtime dependency (see the Dockerfile), so
// PDF work shells out instead of pulling in a Go PDF library. Every wrapper
// looks the binary up first and reports the missing-package hint, because a
// deployment without poppler is the common cause of failure here.
package pdftool

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// InfoTimeout bounds pdfinfo, which only reads the document catalog.
	InfoTimeout = 15 * time.Second
	// RenderTimeout bounds pdftoppm. Rendering every page of a long scan is the
	// slowest thing here, so it gets the largest budget.
	RenderTimeout = 5 * time.Minute
	// TextTimeout bounds one pdftotext page extraction.
	TextTimeout = 30 * time.Second
	// AllTextTimeout bounds one pdftotext run over a whole document, which costs
	// far more than a single page.
	AllTextTimeout = 2 * time.Minute
	// ExtractTimeout bounds the pdfseparate + pdfunite pair for one page range.
	ExtractTimeout = 60 * time.Second
)

// pageSeparator is the form feed pdftotext writes after every page, which is how
// a whole-file extraction is cut back into per-page text.
const pageSeparator = "\f"

// ErrNotPDF is returned for a path that does not carry a .pdf extension.
var ErrNotPDF = fmt.Errorf("pdftool: not a PDF file")

// lookPath resolves a poppler binary, reporting the package to install.
func lookPath(binary string) (string, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("pdftool: %s not found (install poppler-utils): %w", binary, err)
	}
	return path, nil
}

// run executes cmd and returns its standard output, folding standard error into
// the error so a poppler message ("Syntax Error: ...") reaches the caller
// instead of just an exit code.
//
// The two streams are kept apart on purpose: poppler prints warnings about
// damaged files ("Internal Error: xref num 1 not found") to standard error even
// on success, and folding those into the output would count a warning as
// extracted page text — which is enough to make a scan look born-digital.
func run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	path, err := lookPath(binary)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("pdftool: %s: %w: %s", binary, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// RequirePDF rejects a path that is not a PDF before any binary is invoked.
func RequirePDF(pdfPath string) error {
	if strings.ToLower(filepath.Ext(pdfPath)) != ".pdf" {
		return ErrNotPDF
	}
	return nil
}

// PageCount reports how many pages pdfPath holds. A failure here also means the
// file is not a PDF poppler can read, so callers use it as a validity check.
func PageCount(ctx context.Context, pdfPath string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, InfoTimeout)
	defer cancel()

	output, err := run(ctx, "pdfinfo", pdfPath)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(output), "\n") {
		rest, ok := strings.CutPrefix(line, "Pages:")
		if !ok {
			continue
		}
		count, convErr := strconv.Atoi(strings.TrimSpace(rest))
		if convErr != nil {
			return 0, fmt.Errorf("pdftool: unreadable page count %q: %w", strings.TrimSpace(rest), convErr)
		}
		if count <= 0 {
			return 0, fmt.Errorf("pdftool: document reports %d pages", count)
		}
		return count, nil
	}
	return 0, fmt.Errorf("pdftool: pdfinfo reported no page count")
}

// RenderPages renders every page of pdfPath into outDir as page-<n>.png, scaled
// so the longest edge is maxEdge pixels, and returns the paths in page order.
//
// One pdftoppm call renders the whole file; invoking it per page would pay the
// document parse cost again for every page. pdftoppm zero-pads the page suffix
// to the width of the highest page number (page-01.png for a 12 page file), so
// the outputs are read back in sorted order and renamed to unpadded names that
// callers can address directly by page number.
func RenderPages(ctx context.Context, pdfPath, outDir string, maxEdge, pageCount int) ([]string, error) {
	if err := RequirePDF(pdfPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, fmt.Errorf("pdftool: prepare render dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, RenderTimeout)
	defer cancel()

	stagePrefix := filepath.Join(outDir, "raw")
	if _, err := run(ctx, "pdftoppm",
		"-png",
		"-scale-to", strconv.Itoa(maxEdge),
		pdfPath,
		stagePrefix,
	); err != nil {
		return nil, err
	}

	rendered, err := filepath.Glob(stagePrefix + "-*.png")
	if err != nil {
		return nil, fmt.Errorf("pdftool: list rendered pages: %w", err)
	}
	// Zero padding is uniform within one run, so a plain sort is page order.
	sort.Strings(rendered)
	if len(rendered) != pageCount {
		for _, path := range rendered {
			os.Remove(path)
		}
		return nil, fmt.Errorf("pdftool: rendered %d pages, expected %d", len(rendered), pageCount)
	}

	paths := make([]string, 0, len(rendered))
	for i, src := range rendered {
		dst := PagePNGPath(outDir, i+1)
		if err := os.Rename(src, dst); err != nil {
			return nil, fmt.Errorf("pdftool: name rendered page %d: %w", i+1, err)
		}
		paths = append(paths, dst)
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

	ctx, cancel := context.WithTimeout(ctx, RenderTimeout)
	defer cancel()

	// -singlefile makes pdftoppm write exactly outPrefix+".png" with no page
	// suffix, so the output path is known without listing the directory.
	outPrefix := strings.TrimSuffix(outPath, ".png")
	_, err := run(ctx, "pdftoppm",
		"-png",
		"-f", strconv.Itoa(page),
		"-l", strconv.Itoa(page),
		"-scale-to", strconv.Itoa(maxEdge),
		"-singlefile",
		pdfPath,
		outPrefix,
	)
	return err
}

// PagePNGPath is where RenderPages puts the PNG for a 1-based page number.
func PagePNGPath(outDir string, page int) string {
	return filepath.Join(outDir, fmt.Sprintf("page-%d.png", page))
}

// PageText extracts the text layer of one page. It returns "" for a scanned
// page, which is how callers tell born-digital pages from images.
func PageText(ctx context.Context, pdfPath string, page int) (string, error) {
	if err := RequirePDF(pdfPath); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, TextTimeout)
	defer cancel()

	// -layout keeps columns readable, which matters when the text is fed to a
	// model that has to recognize letterheads and totals.
	output, err := run(ctx, "pdftotext",
		"-f", strconv.Itoa(page),
		"-l", strconv.Itoa(page),
		"-layout",
		pdfPath,
		"-",
	)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// AllPagesText extracts the text layer of every page in one pdftotext run and
// returns exactly pageCount entries, one per page in page order.
//
// One call rather than one per page: pdftotext re-parses the whole document
// every time it starts, so a per-page loop pays that cost pageCount times.
// pdftotext writes a form feed after each page, which is what the output is cut
// on.
func AllPagesText(ctx context.Context, pdfPath string, pageCount int) ([]string, error) {
	if err := RequirePDF(pdfPath); err != nil {
		return nil, err
	}
	if pageCount < 1 {
		return nil, fmt.Errorf("pdftool: invalid page count %d", pageCount)
	}
	ctx, cancel := context.WithTimeout(ctx, AllTextTimeout)
	defer cancel()

	// -layout keeps columns readable, which matters when the text is fed to a
	// model that has to recognize letterheads and totals.
	output, err := run(ctx, "pdftotext", "-layout", pdfPath, "-")
	if err != nil {
		return nil, err
	}

	// The trailing separator after the last page would otherwise read as an
	// extra empty page.
	text := strings.TrimSuffix(string(output), pageSeparator)
	pages := strings.Split(text, pageSeparator)

	// A damaged file can yield fewer (or more) sections than it has pages, and
	// callers index by page number, so the result is squared up either way
	// instead of failing the whole extraction over a missing text layer.
	out := make([]string, pageCount)
	for i := range out {
		if i < len(pages) {
			out[i] = strings.TrimSpace(pages[i])
		}
	}
	return out, nil
}

// ExtractRange writes pages from..to of pdfPath to outPath as a new PDF.
//
// pdfseparate copies the original page objects rather than re-rasterizing, so
// the text layer and image quality survive; it only writes one page per file,
// so a multi-page range is stitched back together with pdfunite.
func ExtractRange(ctx context.Context, pdfPath string, from, to int, outPath string) error {
	if err := RequirePDF(pdfPath); err != nil {
		return err
	}
	if from < 1 || to < from {
		return fmt.Errorf("pdftool: invalid page range %d-%d", from, to)
	}

	ctx, cancel := context.WithTimeout(ctx, ExtractTimeout)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "paperless-pdfrange-*")
	if err != nil {
		return fmt.Errorf("pdftool: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if _, err := run(ctx, "pdfseparate",
		"-f", strconv.Itoa(from),
		"-l", strconv.Itoa(to),
		pdfPath,
		filepath.Join(tmpDir, "page-%d.pdf"),
	); err != nil {
		return err
	}

	pages, err := filepath.Glob(filepath.Join(tmpDir, "page-*.pdf"))
	if err != nil {
		return fmt.Errorf("pdftool: list extracted pages: %w", err)
	}
	if len(pages) == 0 {
		return fmt.Errorf("pdftool: pdfseparate produced no pages for range %d-%d", from, to)
	}
	if len(pages) == 1 {
		if err := movePDF(pages[0], outPath); err != nil {
			return err
		}
		return canonicalizeFileID(outPath)
	}

	// pdfseparate names files by source page number, so sorting numerically
	// keeps the parts in reading order before they are merged.
	sortByPageNumber(pages)
	args := append(pages, outPath)
	if _, err := run(ctx, "pdfunite", args...); err != nil {
		return err
	}
	return canonicalizeFileID(outPath)
}

// movePDF relocates src to dst, falling back to a copy across filesystems
// (the temp dir and the data dir are not always on the same device).
func movePDF(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("pdftool: read extracted page: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("pdftool: write extracted page: %w", err)
	}
	return nil
}

// sortByPageNumber orders page-<n>.pdf paths by n, so page-10 follows page-9
// instead of page-1 as a lexical sort would have it.
func sortByPageNumber(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		return pageNumberOf(paths[i]) < pageNumberOf(paths[j])
	})
}

func pageNumberOf(path string) int {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	_, digits, ok := strings.Cut(name, "-")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return n
}
