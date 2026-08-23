package pdftool

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// canonicalVerifyTimeout bounds the re-open check after the trailer rewrite.
//
// It is deliberately independent of the caller's context: verification runs at
// the tail of ExtractRange's budget, so reusing that nearly spent deadline would
// time the check out and hand back a randomly-identified file — exactly the
// duplicate that canonicalization exists to prevent.
const canonicalVerifyTimeout = 15 * time.Second

// canonicalID is the fixed trailer /ID written in place of poppler's random one.
// The letters never need escaping inside a PDF literal string, so the array is
// always exactly this many bytes.
var canonicalID = []byte("[(AAAAAAAAAAAAAAAA) (AAAAAAAAAAAAAAAA)]")

// canonicalizeFileID rewrites the trailer /ID of a poppler-produced PDF to a
// fixed value.
//
// pdfseparate and pdfunite stamp a fresh random /ID into every file they write,
// so extracting the same pages twice yields different bytes and the app's
// exact-duplicate check never fires — re-splitting a scan would silently create
// a second copy of every part.
//
// The rewrite changes the length of the trailer, which is only safe because the
// trailer sits after the cross-reference table: nothing in the file points past
// it. That is checked, not assumed — the rewrite is skipped unless the /ID lies
// beyond the offset startxref names. The result is then verified, and the
// original bytes are restored if it does not open: a file that hashes
// differently is a far smaller problem than one that cannot be read.
//
// Falling back is logged rather than returned as an error, because the file is
// still perfectly usable — but it will not deduplicate against a later split of
// the same source, so the reason has to be visible somewhere.
func canonicalizeFileID(path string) error {
	original, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("pdftool: read for canonicalization: %w", err)
	}

	rewritten, changed := canonicalizeTrailerID(original)
	if !changed {
		// Either the file is already canonical (nothing to do) or its trailer is
		// not the shape the rewrite understands, which is worth knowing about:
		// this part will not deduplicate against a later split of the same scan.
		if !bytes.Contains(original, canonicalID) {
			slog.Default().Warn("pdf file id left as poppler wrote it",
				"component", "pdftool",
				"path", path,
				"reason", "no rewritable trailer /ID",
			)
		}
		return nil
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		return fmt.Errorf("pdftool: write canonicalized file: %w", err)
	}

	// A fresh context, not the caller's: see canonicalVerifyTimeout.
	ctx, cancel := context.WithTimeout(context.Background(), canonicalVerifyTimeout)
	defer cancel()
	if _, err := PageCount(ctx, path); err != nil {
		slog.Default().Warn("canonicalized pdf did not verify, keeping the original",
			"component", "pdftool",
			"path", path,
			"error", err,
		)
		if writeErr := os.WriteFile(path, original, 0o600); writeErr != nil {
			return fmt.Errorf("pdftool: restore after failed canonicalization: %w", writeErr)
		}
	}
	return nil
}

// canonicalizeTrailerID replaces the /ID array of the trailer with a fixed one,
// reporting whether anything changed.
func canonicalizeTrailerID(data []byte) ([]byte, bool) {
	xrefOffset, ok := startxrefOffset(data)
	if !ok {
		return data, false
	}

	// The trailer is the last dictionary in the file, so the last /ID is it —
	// and it must live past the cross-reference table for the edit to be safe.
	idAt := bytes.LastIndex(data, []byte("/ID"))
	if idAt < xrefOffset {
		return data, false
	}

	start, end, ok := arrayBounds(data, idAt+len("/ID"))
	if !ok {
		return data, false
	}
	if bytes.Equal(data[start:end], canonicalID) {
		return data, false
	}

	out := make([]byte, 0, len(data)+len(canonicalID))
	out = append(out, data[:start]...)
	out = append(out, canonicalID...)
	out = append(out, data[end:]...)
	return out, true
}

// startxrefOffset reads the cross-reference offset the file's last startxref
// names.
func startxrefOffset(data []byte) (int, bool) {
	at := bytes.LastIndex(data, []byte("startxref"))
	if at < 0 {
		return 0, false
	}
	rest := data[at+len("startxref"):]
	digits := bytes.TrimLeft(rest, " \r\n\t")
	cut := 0
	for cut < len(digits) && digits[cut] >= '0' && digits[cut] <= '9' {
		cut++
	}
	if cut == 0 {
		return 0, false
	}
	offset, err := strconv.Atoi(string(digits[:cut]))
	if err != nil || offset <= 0 || offset >= len(data) {
		return 0, false
	}
	return offset, true
}

// arrayBounds returns the half-open byte range of the "[...]" array starting at
// or after from. Literal strings inside it are skipped, so a "]" in a string
// does not end the array early.
func arrayBounds(data []byte, from int) (start, end int, ok bool) {
	for start = from; start < len(data); start++ {
		if data[start] == '[' {
			break
		}
		// Anything other than whitespace before the array means this is not the
		// shape we expect, and guessing is how files get corrupted.
		switch data[start] {
		case ' ', '\r', '\n', '\t':
		default:
			return 0, 0, false
		}
	}
	if start >= len(data) {
		return 0, 0, false
	}

	inString := false
	for i := start + 1; i < len(data); i++ {
		switch {
		case inString && data[i] == '\\':
			i++
		case inString && data[i] == ')':
			inString = false
		case inString:
		case data[i] == '(':
			inString = true
		case data[i] == ']':
			return start, i + 1, true
		}
	}
	return 0, 0, false
}
