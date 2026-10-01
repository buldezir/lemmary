package fulltext

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func Build(app core.App, rec *core.Record) map[string]any {
	return buildWith(newNameCache(app), rec)
}

// nameCache memoizes named-entity lookups: a full rebuild otherwise looks the
// same handful of entities up thousands of times. It also holds the readers of
// each document, for the same reason.
type nameCache struct {
	app     core.App
	names   map[string]string
	readers map[string][]string
	values  map[string]models.FieldValues
}

func newNameCache(app core.App) *nameCache {
	return &nameCache{app: app, names: map[string]string{}}
}

// preloadReaders reads every share once, for the callers that walk the whole
// documents table. Without it each document costs its own query.
func (c *nameCache) preloadReaders() {
	if c == nil || c.app == nil || c.readers != nil {
		return
	}
	c.readers = map[string][]string{}
	shares, err := c.app.FindAllRecords(CollectionShares)
	if err != nil {
		// A missing or unreadable share table means no shares, not no index.
		return
	}
	for _, share := range shares {
		docID := share.GetString("document")
		c.readers[docID] = append(c.readers[docID], share.GetString("user"))
	}
}

// preloadValues reads every document's field values once, for the same callers.
func (c *nameCache) preloadValues() {
	if c == nil || c.app == nil || c.values != nil {
		return
	}
	values, err := models.LoadAllFieldValues(c.app)
	if err != nil {
		// Unreadable values index as none rather than stopping the rebuild.
		values = map[string]models.FieldValues{}
	}
	c.values = values
}

func (c *nameCache) valuesOf(docID string) models.FieldValues {
	if c == nil {
		return nil
	}
	if c.values != nil {
		return c.values[docID]
	}
	if c.app == nil {
		return nil
	}
	values, err := models.LoadDocumentFieldValues(c.app, docID)
	if err != nil {
		return nil
	}
	return values
}

// readersOf is the owner plus everyone the document is shared with. Indexing
// them all in one field is what lets every search keep its single term filter.
func (c *nameCache) readersOf(rec *core.Record) []string {
	owner := rec.GetString("user")
	if c == nil || c.app == nil {
		return []string{owner}
	}
	if c.readers != nil {
		return append([]string{owner}, c.readers[rec.Id]...)
	}
	shares, err := c.app.FindAllRecords(CollectionShares, dbx.HashExp{"document": rec.Id})
	if err != nil {
		return []string{owner}
	}
	readers := make([]string, 0, len(shares)+1)
	readers = append(readers, owner)
	for _, share := range shares {
		readers = append(readers, share.GetString("user"))
	}
	return readers
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

	values := names.valuesOf(rec.Id)
	typeID := values.OptionID(models.DocumentTypeFieldID)
	corrID := values.OptionID(models.CorrespondentFieldID)
	typeName := strings.TrimSpace(values.OptionName(models.DocumentTypeFieldID))
	corrName := strings.TrimSpace(values.OptionName(models.CorrespondentFieldID))
	people := models.PeopleOrOrganizations(rec)

	title := strings.TrimSpace(rec.GetString("title"))
	summary := strings.TrimSpace(rec.GetString("summary"))
	ocr := strings.TrimSpace(rec.GetString("ocr_text"))
	peopleText := strings.Join(people, " ")
	tagNameText := strings.Join(tagNames, " ")

	allParts := []string{
		title, summary, ocr, tagNameText, typeName, corrName, peopleText, customFieldsText(values),
	}

	doc := map[string]any{
		FieldUser:              names.readersOf(rec),
		FieldOwner:             rec.GetString("user"),
		FieldProcessingStatus:  rec.GetString("processing_status"),
		FieldDocumentType:      typeID,
		FieldCorrespondent:     corrID,
		FieldTags:              tagIDs,
		FieldTitle:             title,
		FieldSummary:           summary,
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

// customFieldsText is searchable through FieldAll only: a value typed by hand
// is often the one thing about a document its OCR text does not say. Options
// are indexed on their own fields above.
func customFieldsText(values models.FieldValues) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		switch v.Type {
		case models.CustomFieldOption:
		case models.CustomFieldNumber:
			parts = append(parts, strconv.FormatFloat(v.Number, 'f', -1, 64))
		case models.CustomFieldDate:
			parts = append(parts, v.Date)
		default:
			parts = append(parts, v.Text)
		}
	}
	slices.Sort(parts)
	return joinNonEmpty(parts)
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
