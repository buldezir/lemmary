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
// The *Original fields carry the pre-translation wording. They are embedded
// beside the translated ones, and only when they differ, because the archive is
// bilingual on both sides: a document written in German is stored with an
// English summary, and the question about it can arrive in either language.
// This is the same pairing the keyword index already carries.
type Header struct {
	Title           string
	TitleOriginal   string
	Purpose         string
	PurposeOriginal string
	Summary         string
	SummaryOriginal string
	DocumentType    string
	Correspondent   string
	Date            string
	Tags            []string
	People          []string
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

	// The original wording is written only when it differs from the translation,
	// so a monolingual archive does not spend half its header window saying
	// everything twice.
	writeOriginal := func(label, original, translated string) {
		if !strings.EqualFold(strings.TrimSpace(original), strings.TrimSpace(translated)) {
			write(label, original)
		}
	}

	write("Title", h.Title)
	writeOriginal("Original title", h.TitleOriginal, h.Title)
	write("Type", h.DocumentType)
	write("Correspondent", h.Correspondent)
	write("Date", h.Date)
	write("Tags", joinList(h.Tags))
	write("People or organizations", joinList(h.People))
	write("Purpose", h.Purpose)
	writeOriginal("Original purpose", h.PurposeOriginal, h.Purpose)
	write("Summary", h.Summary)
	writeOriginal("Original summary", h.SummaryOriginal, h.Summary)

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
