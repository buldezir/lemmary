// Package testpdf builds small, valid PDF files in memory for tests.
//
// The PDF work in this repo shells out to poppler, so its tests need real PDFs
// rather than fixtures poppler would reject. Generating them keeps the page
// count and the per-page text under the test's control and avoids committing
// binary fixtures for every shape a test needs.
package testpdf

import (
	"bytes"
	"fmt"
)

// Multipage returns an uncompressed PDF with pageCount pages, each carrying the
// line "Page <n>" plus the caller's extra lines, so page-level text extraction
// has something recognizable to find.
func Multipage(pageCount int, extraLines ...string) []byte {
	if pageCount < 1 {
		pageCount = 1
	}

	// Object ids: 1 catalog, 2 page tree, 3 font, then two objects per page.
	const firstPageObj = 4
	objects := make([]string, 0, 3+2*pageCount)

	kids := &bytes.Buffer{}
	for i := 0; i < pageCount; i++ {
		if i > 0 {
			kids.WriteString(" ")
		}
		fmt.Fprintf(kids, "%d 0 R", firstPageObj+2*i)
	}

	objects = append(objects,
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), pageCount),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	)

	for i := 0; i < pageCount; i++ {
		pageObj := firstPageObj + 2*i
		contentObj := pageObj + 1
		objects = append(objects, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] "+
				"/Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>",
			contentObj,
		))
		objects = append(objects, contentStream(pageText(i+1, extraLines)))
	}

	return assemble(objects)
}

func pageText(page int, extraLines []string) []string {
	lines := make([]string, 0, len(extraLines)+1)
	lines = append(lines, fmt.Sprintf("Page %d", page))
	return append(lines, extraLines...)
}

func contentStream(lines []string) string {
	body := &bytes.Buffer{}
	body.WriteString("BT /F1 18 Tf 72 760 Td 24 TL\n")
	for _, line := range lines {
		fmt.Fprintf(body, "(%s) Tj T*\n", escapeText(line))
	}
	body.WriteString("ET\n")
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", body.Len(), body.String())
}

// escapeText escapes the three characters that are special inside a PDF string.
func escapeText(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '(', ')', '\\':
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

// assemble writes the objects with a cross-reference table whose offsets are
// computed from the bytes actually emitted, which is what makes the result a
// PDF poppler will open.
func assemble(objects []string) []byte {
	buf := &bytes.Buffer{}
	buf.WriteString("%PDF-1.4\n")

	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(buf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}

	xrefOffset := buf.Len()
	fmt.Fprintf(buf, "xref\n0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(buf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(buf,
		"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, xrefOffset,
	)
	return buf.Bytes()
}

// Blank returns an uncompressed PDF with pageCount pages that carry no text at
// all — what a scanned document looks like to a text extractor.
func Blank(pageCount int) []byte {
	if pageCount < 1 {
		pageCount = 1
	}

	const firstPageObj = 3
	kids := &bytes.Buffer{}
	for i := 0; i < pageCount; i++ {
		if i > 0 {
			kids.WriteString(" ")
		}
		fmt.Fprintf(kids, "%d 0 R", firstPageObj+2*i)
	}

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), pageCount),
	}
	for i := 0; i < pageCount; i++ {
		contentObj := firstPageObj + 2*i + 1
		objects = append(objects, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << >> /Contents %d 0 R >>",
			contentObj,
		))
		objects = append(objects, "<< /Length 0 >>\nstream\n\nendstream")
	}
	return assemble(objects)
}

// MixedText returns a PDF of pageCount pages where only the first textPages
// carry text, the shape of a scan with a born-digital cover sheet.
func MixedText(pageCount, textPages int) []byte {
	if pageCount < 1 {
		pageCount = 1
	}

	const firstPageObj = 4
	kids := &bytes.Buffer{}
	for i := 0; i < pageCount; i++ {
		if i > 0 {
			kids.WriteString(" ")
		}
		fmt.Fprintf(kids, "%d 0 R", firstPageObj+2*i)
	}

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), pageCount),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i := 0; i < pageCount; i++ {
		contentObj := firstPageObj + 2*i + 1
		objects = append(objects, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] "+
				"/Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>",
			contentObj,
		))
		if i < textPages {
			objects = append(objects, contentStream(pageText(i+1, []string{"Invoice INV-1001", "Acme Plumbing GmbH"})))
		} else {
			objects = append(objects, "<< /Length 0 >>\nstream\n\nendstream")
		}
	}
	return assemble(objects)
}
