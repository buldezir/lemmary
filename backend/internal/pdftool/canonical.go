package pdftool

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"time"
)

// canonicalVerifyTimeout bounds the re-open check after the trailer rewrite.
// Independent of the caller's context: verification runs at the tail of
// ExtractRange's budget, so reusing that nearly spent deadline would time the
// check out and hand back a randomly-identified file.
const canonicalVerifyTimeout = 15 * time.Second

// canonicalID is the fixed trailer /ID written in place of PDFium's random one.
// The letters never need escaping inside a PDF literal string, so the array is
// always exactly this many bytes.
var canonicalID = []byte("[(AAAAAAAAAAAAAAAA) (AAAAAAAAAAAAAAAA)]")

// canonicalDate replaces the digits of the /CreationDate PDFium stamps into a
// new document. Same length as what it replaces, so no offset moves.
const canonicalDate = "19700101000000"

var (
	creationDate = regexp.MustCompile(`/CreationDate\s*\(D:(\d{14})`)
	trailerInfo  = regexp.MustCompile(`/Info\s+(\d+)\s+0\s+R`)
)

// canonicalizeFileID rewrites the parts of a PDFium-written PDF that change on
// every run to fixed values: the trailer /ID always, and with newDocument the
// /CreationDate PDFium stamps into the Info dictionary of a document it
// created. A saved existing document (Decrypt) keeps its own Info as it was.
//
// Without it, extracting the same pages twice yields different bytes and the
// app's exact-duplicate check never fires: re-splitting a scan would silently
// create a second copy of every part.
//
// The /ID rewrite changes the length of the trailer, which is only safe because
// the trailer sits after the cross-reference table. That is checked, not
// assumed: the rewrite is skipped unless the /ID lies beyond the offset
// startxref names, and the result is verified and the original bytes restored
// if it does not open. The date keeps its length, so no offset moves.
//
// Falling back is logged rather than returned as an error: the file is still
// usable, but it will not deduplicate against a later split of the same source.
func canonicalizeFileID(ctx context.Context, path string, newDocument bool) error {
	original, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("pdftool: read for canonicalization: %w", err)
	}

	rewritten, changed := canonicalizeTrailerID(original)
	if newDocument {
		if dated, ok := canonicalizeCreationDate(rewritten); ok {
			rewritten, changed = dated, true
		}
	}
	if !changed {
		// Either the file is already canonical (nothing to do) or its trailer is
		// not the shape the rewrite understands, which is worth knowing about:
		// this part will not deduplicate against a later split of the same scan.
		if !bytes.Contains(original, canonicalID) {
			slog.Default().Warn("pdf file id left as pdfium wrote it",
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

	// Detached from the caller's deadline: see canonicalVerifyTimeout.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), canonicalVerifyTimeout)
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

// canonicalizeCreationDate pins the /CreationDate of the Info dictionary the
// trailer names to canonicalDate, reporting whether anything changed. Dates
// elsewhere -- an annotation's, say -- were copied from the source and are
// already stable.
func canonicalizeCreationDate(data []byte) ([]byte, bool) {
	xrefOffset, ok := startxrefOffset(data)
	if !ok {
		return data, false
	}
	info := trailerInfo.FindSubmatch(data[xrefOffset:])
	if info == nil {
		return data, false
	}
	start, end, ok := objectBounds(data[:xrefOffset], string(info[1]))
	if !ok {
		return data, false
	}
	m := creationDate.FindSubmatchIndex(data[start:end])
	if m == nil || string(data[start+m[2]:start+m[3]]) == canonicalDate {
		return data, false
	}
	out := bytes.Clone(data)
	copy(out[start+m[2]:start+m[3]], canonicalDate)
	return out, true
}

// objectBounds returns the byte range of the last "<number> 0 obj ... endobj"
// in data, the copy a reader uses.
func objectBounds(data []byte, number string) (start, end int, ok bool) {
	header := regexp.MustCompile(`(?m)^` + number + `\s+0\s+obj\b`)
	all := header.FindAllIndex(data, -1)
	if all == nil {
		return 0, 0, false
	}
	start = all[len(all)-1][0]
	length := bytes.Index(data[start:], []byte("endobj"))
	if length < 0 {
		return 0, 0, false
	}
	return start, start + length, true
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
	_, after, ok := bytes.CutLast(data, []byte("startxref"))
	if !ok {
		return 0, false
	}
	rest := after
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
