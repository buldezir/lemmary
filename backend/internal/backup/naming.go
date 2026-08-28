package backup

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxTitleBytes caps the sanitized title portion of an entry name, keeping
// "[id] title.metadata.json" — the longest name a document produces — under the
// 255-byte limit most filesystems impose on a single path element.
const maxTitleBytes = 120

// EntryBase returns "[id] title" with a filesystem-safe title. Every entry a
// document owns is this base plus a suffix, which is what groups them.
func EntryBase(id, title string) string {
	return "[" + id + "] " + SanitizeTitle(title)
}

// ParseEntryBase splits "[id] title" back into its parts. It reports false for
// a name that was not produced by EntryBase, so a stray entry in the archive is
// ignored rather than imported as a document with a nonsense id.
func ParseEntryBase(base string) (id, title string, ok bool) {
	if !strings.HasPrefix(base, "[") {
		return "", "", false
	}
	end := strings.Index(base, "] ")
	if end <= 1 {
		return "", "", false
	}
	id = base[1:end]
	if id == "" || strings.ContainsAny(id, "[]") {
		return "", "", false
	}
	return id, base[end+2:], true
}

// SanitizeTitle strips what a filesystem or zip reader would choke on.
func SanitizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Untitled"
	}
	var b strings.Builder
	b.Grow(len(title))
	prevSpace := false
	for _, r := range title {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			continue
		case r == 0 || unicode.IsControl(r):
			continue
		case unicode.IsSpace(r):
			if prevSpace || b.Len() == 0 {
				continue
			}
			b.WriteByte(' ')
			prevSpace = true
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "Untitled"
	}
	// Avoid names that are only "." / ".." after sanitizing.
	if out == "." || out == ".." {
		return "Untitled"
	}
	return truncateUTF8Bytes(out, maxTitleBytes)
}

// truncateUTF8Bytes shortens s to at most maxBytes without splitting a rune.
func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	truncated := s[:maxBytes]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return strings.TrimSpace(truncated)
}
