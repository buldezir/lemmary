package chunk

import (
	"strings"

	"lemmary/backend/internal/strutil"
)

// HeaderMaxRunes bounds the metadata passage. Summaries are already short; the
// cap is here so a pathological tag list cannot push the header past an
// embedding model's input window.
const HeaderMaxRunes = 2000

// Header is a document's metadata rendered as one passage and embedded as
// ordinal 0.
//
// It exists because the body chunks carry only OCR text, and a question is
// often asked in the vocabulary of the metadata rather than of the scan: "the
// electricity contract" matches a summary and a tag long before it matches the
// wording on the page. Embedding the metadata as its own passage is what lets a
// document be found by what it is, not only by what it says.
type Header struct {
	Title         string
	TitleOriginal string
	Purpose       string
	Summary       string
	DocumentType  string
	Correspondent string
	Date          string
	Tags          []string
	People        []string
}

// Text renders the header, or "" when there is no metadata worth embedding.
// The field labels are part of the embedded text on purpose: they are what
// gives "Rechnung" in a type field a different neighbourhood from "Rechnung" in
// a title.
func (h Header) Text() string {
	var b strings.Builder
	write := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(value)
	}

	write("Title", h.Title)
	if !strings.EqualFold(strings.TrimSpace(h.TitleOriginal), strings.TrimSpace(h.Title)) {
		write("Original title", h.TitleOriginal)
	}
	write("Type", h.DocumentType)
	write("Correspondent", h.Correspondent)
	write("Date", h.Date)
	write("Tags", joinList(h.Tags))
	write("People or organizations", joinList(h.People))
	write("Purpose", h.Purpose)
	write("Summary", h.Summary)

	return strutil.TruncateRunes(b.String(), HeaderMaxRunes)
}

func joinList(values []string) string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}
