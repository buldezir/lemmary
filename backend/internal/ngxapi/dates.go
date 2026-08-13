package ngxapi

import (
	"strings"
	"time"
)

// formatNgxDateTime converts PocketBase timestamps to ISO-8601 with a T separator,
// which swift-paperless and paperless-ngx clients expect for added/modified fields.
func formatNgxDateTime(datetime string) string {
	if datetime == "" {
		return ""
	}

	layouts := []string{
		"2006-01-02 15:04:05.000Z",
		"2006-01-02 15:04:05Z",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, datetime); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}

	if strings.Contains(datetime, " ") {
		return strings.Replace(datetime, " ", "T", 1)
	}
	return datetime
}

// formatNgxCreatedDate formats document created dates for API consumers.
func formatNgxCreatedDate(docDate string) string {
	if docDate == "" {
		return ""
	}
	if !strings.ContainsAny(docDate, " T") {
		return docDate + "T00:00:00Z"
	}
	return formatNgxDateTime(docDate)
}

func createdDateOnly(datetime string) string {
	if datetime == "" {
		return ""
	}
	if t, err := time.Parse("2006-01-02 15:04:05.000Z", datetime); err == nil {
		return t.Format("2006-01-02")
	}
	if len(datetime) >= 10 {
		return datetime[:10]
	}
	return datetime
}
