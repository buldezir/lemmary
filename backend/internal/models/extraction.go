package models

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var xmlBlockRE = regexp.MustCompile(`(?s)<[^>]+>.*?</[^>]+>`)

type ExtractedMetadata struct {
	Title                   string   `json:"title"`
	TitleTranslated         string   `json:"title_translated"`
	Purpose                 string   `json:"purpose"`
	PurposeTranslated       string   `json:"purpose_translated"`
	DocumentDate            string   `json:"document_date"`
	DocumentType            string   `json:"document_type"`
	DocumentTypeTranslated  string   `json:"document_type_translated"`
	Correspondent           string   `json:"correspondent"`
	CorrespondentTranslated string   `json:"correspondent_translated"`
	Tags                    []string `json:"tags"`
	TagsTranslated          []string `json:"tags_translated"`
	PeopleOrOrganizations   []string `json:"people_or_organizations"`
	Summary                 string   `json:"summary"`
	SummaryTranslated       string   `json:"summary_translated"`
	Confidence              float64  `json:"confidence"`
}

func (m *ExtractedMetadata) Populated() bool {
	return m != nil && strings.TrimSpace(m.Title) != ""
}

func (m *ExtractedMetadata) Validate() error {
	if strings.TrimSpace(m.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if m.Confidence < 0 || m.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	// Post-normalization invariant. ParseExtractedMetadata runs Normalize
	// first, so this only fires for metadata built directly in code.
	if m.DocumentDate != "" {
		if _, err := time.Parse(documentDateLayout, m.DocumentDate); err != nil {
			return fmt.Errorf("document_date must be YYYY-MM-DD: %w", err)
		}
	}
	return nil
}

// Normalize repairs best-effort fields in place and returns human-readable
// notes about anything it changed or dropped, so a caller with a logger can
// report the repair.
//
// document_date is optional metadata: a model that answers "2026" instead of a
// full calendar date should not sink an otherwise good extraction, so an
// unusable value is dropped rather than turned into an error.
func (m *ExtractedMetadata) Normalize() []string {
	var notes []string

	raw := strings.TrimSpace(m.DocumentDate)
	normalized, ok := NormalizeDocumentDate(raw)
	switch {
	case !ok:
		notes = append(notes, fmt.Sprintf("document_date %q is not a usable date; dropped", raw))
	case normalized != m.DocumentDate && raw != "":
		notes = append(notes, fmt.Sprintf("document_date %q normalized to %q", raw, normalized))
	}
	m.DocumentDate = normalized

	return notes
}

func ParseExtractedMetadata(raw string) (*ExtractedMetadata, error) {
	metadata, _, err := ParseExtractedMetadataWithNotes(raw)
	return metadata, err
}

// ParseExtractedMetadataWithNotes parses a model response and additionally
// returns the notes from Normalize, for callers that can log them.
func ParseExtractedMetadataWithNotes(raw string) (*ExtractedMetadata, []string, error) {
	raw = normalizeExtractionJSON(raw)

	var metadata ExtractedMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, nil, fmt.Errorf("invalid extraction JSON: %w", err)
	}
	notes := metadata.Normalize()
	if err := metadata.Validate(); err != nil {
		return nil, notes, err
	}
	return &metadata, notes, nil
}

func normalizeExtractionJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// Some models (e.g. MiniMax M3) wrap reasoning in XML-like tags before JSON.
	for strings.HasPrefix(raw, "<") {
		stripped := strings.TrimSpace(xmlBlockRE.ReplaceAllString(raw, ""))
		if stripped == raw {
			break
		}
		raw = stripped
	}

	if !strings.HasPrefix(raw, "{") {
		if jsonObj := extractJSONObject(raw); jsonObj != "" {
			raw = jsonObj
		}
	}

	return strings.TrimSpace(raw)
}

func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}

	return ""
}
