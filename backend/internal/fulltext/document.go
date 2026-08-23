package fulltext

import (
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// Build converts a document record into the Bleve document to index.
func Build(app core.App, rec *core.Record) map[string]any {
	return buildWith(newNameCache(app), rec)
}

// nameCache memoizes named-entity lookups. A single document costs one query per
// tag plus one each for its type and correspondent; over a full rebuild the same
// handful of entities is looked up thousands of times, so the cache turns that
// fan-out into one query per distinct entity.
type nameCache struct {
	app   core.App
	names map[string]string
}

func newNameCache(app core.App) *nameCache {
	return &nameCache{app: app, names: map[string]string{}}
}

func (c *nameCache) lookup(collection, id string) string {
	id = strings.TrimSpace(id)
	if c == nil || c.app == nil || id == "" {
		return ""
	}
	key := collection + "/" + id
	if name, ok := c.names[key]; ok {
		return name
	}
	name := lookupName(c.app, collection, id)
	c.names[key] = name
	return name
}

func buildWith(names *nameCache, rec *core.Record) map[string]any {
	tagIDs := rec.GetStringSlice("tags")
	tagNames := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		if name := names.lookup("tags", id); name != "" {
			tagNames = append(tagNames, name)
		}
	}

	typeID := rec.GetString("document_type")
	corrID := rec.GetString("correspondent")
	typeName := names.lookup("document_types", typeID)
	corrName := names.lookup("correspondents", corrID)
	people := models.PeopleOrOrganizations(rec)

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
