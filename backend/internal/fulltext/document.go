package fulltext

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

func Build(app core.App, rec *core.Record) map[string]any {
	tagIDs := rec.GetStringSlice("tags")
	tagNames := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		if name := lookupName(app, "tags", id); name != "" {
			tagNames = append(tagNames, name)
		}
	}

	typeID := rec.GetString("document_type")
	corrID := rec.GetString("correspondent")
	typeName := lookupName(app, "document_types", typeID)
	corrName := lookupName(app, "correspondents", corrID)
	people := peopleOrOrganizations(rec)

	title := strings.TrimSpace(rec.GetString("title"))
	titleOrig := strings.TrimSpace(rec.GetString("title_original"))
	purpose := strings.TrimSpace(rec.GetString("purpose"))
	purposeOrig := strings.TrimSpace(rec.GetString("purpose_original"))
	summary := strings.TrimSpace(rec.GetString("summary"))
	summaryOrig := strings.TrimSpace(rec.GetString("summary_original"))
	ocr := strings.TrimSpace(rec.GetString("ocr_text"))
	peopleText := strings.Join(people, " ")
	tagNameText := strings.Join(tagNames, " ")

	allParts := []string{
		title, titleOrig, purpose, purposeOrig, summary, summaryOrig,
		ocr, tagNameText, typeName, corrName, peopleText,
	}

	doc := map[string]any{
		FieldUser:              rec.GetString("user"),
		FieldProcessingStatus:  rec.GetString("processing_status"),
		FieldDocumentType:      typeID,
		FieldCorrespondent:     corrID,
		FieldTags:              tagIDs,
		FieldTitle:             title,
		FieldTitleOriginal:     titleOrig,
		FieldPurpose:           purpose,
		FieldPurposeOriginal:   purposeOrig,
		FieldSummary:           summary,
		FieldSummaryOriginal:   summaryOrig,
		FieldOCRText:           ocr,
		FieldTagNames:          tagNameText,
		FieldDocumentTypeName:  typeName,
		FieldCorrespondentName: corrName,
		FieldPeople:            peopleText,
		FieldAll:               joinNonEmpty(allParts),
	}
	if t, ok := parseDocumentDate(rec.GetString("document_date")); ok {
		doc[FieldDocumentDate] = t
	}
	return doc
}

func lookupName(app core.App, collection, id string) string {
	id = strings.TrimSpace(id)
	if app == nil || id == "" {
		return ""
	}
	rec, err := app.FindRecordById(collection, id)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(rec.GetString("name"))
}

func peopleOrOrganizations(record *core.Record) []string {
	raw := record.Get("people_or_organizations")
	switch v := raw.(type) {
	case []string:
		return trimNonEmpty(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err == nil {
			return trimNonEmpty(out)
		}
		return nil
	default:
		return nil
	}
}

func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func joinNonEmpty(parts []string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func parseDocumentDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.000Z",
		"2006-01-02 15:04:05Z",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
