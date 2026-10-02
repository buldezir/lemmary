package models

import (
	"math"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const (
	CustomFieldText   = "text"
	CustomFieldNumber = "number"
	CustomFieldDate   = "date"
	// CustomFieldOption takes its value from a per-user vocabulary in
	// custom_field_options. Only the predefined fields are of this type.
	CustomFieldOption = "option"
	// CustomFieldChoice takes one of the admin's fixed custom_field_choices.
	CustomFieldChoice = "choice"
)

const MaxCustomFieldValueRunes = 500

// JobOverwriteCustomFields is the processing_jobs flag that lets its
// apply_metadata step replace values someone already has, not only fill the
// empty ones.
const JobOverwriteCustomFields = "overwrite_custom_fields"

const (
	CustomFieldsCollection       = "custom_fields"
	CustomFieldOptionsCollection = "custom_field_options"
	CustomFieldValuesCollection  = "custom_field_values"
	CustomFieldChoicesCollection = "custom_field_choices"
)

// The predefined fields' ids are fixed, so code and filters name them directly.
const (
	CorrespondentFieldID = "fcorrespondent0"
	DocumentTypeFieldID  = "fdocumenttype00"
)

// CustomField is one field definition. Values are stored under ID, so a rename
// touches no value.
type CustomField struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	// Choices is set for a choice field only.
	Choices []FieldChoice `json:"choices,omitempty"`
}

// FieldChoice is one allowed value of a choice field.
type FieldChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var (
	CorrespondentField     = CustomField{ID: CorrespondentFieldID, Name: "Correspondent", Type: CustomFieldOption}
	DocumentTypeField      = CustomField{ID: DocumentTypeFieldID, Name: "Document type", Type: CustomFieldOption}
	PredefinedCustomFields = []CustomField{CorrespondentField, DocumentTypeField}
)

// LoadCustomFields reads the admin's fields in the order they were added,
// without the predefined option fields, which every caller handles on its own.
// The collection allows no blank name, but a name of spaces is blank once
// trimmed.
func LoadCustomFields(app core.App) ([]CustomField, error) {
	records, err := app.FindRecordsByFilter(CustomFieldsCollection, "type != {:option}", "created,id", 0, 0,
		map[string]any{"option": CustomFieldOption})
	if err != nil {
		return nil, err
	}
	fields := make([]CustomField, 0, len(records))
	for _, record := range records {
		field := CustomField{
			ID:          record.Id,
			Name:        strings.TrimSpace(record.GetString("name")),
			Type:        record.GetString("type"),
			Description: strings.TrimSpace(record.GetString("description")),
		}
		if field.Name != "" {
			fields = append(fields, field)
		}
	}
	return fields, attachChoices(app, fields)
}

func attachChoices(app core.App, fields []CustomField) error {
	records, err := app.FindRecordsByFilter(CustomFieldChoicesCollection, "", "created,id", 0, 0)
	if err != nil {
		return err
	}
	byField := map[string][]FieldChoice{}
	for _, record := range records {
		field := record.GetString("field")
		byField[field] = append(byField[field], FieldChoice{ID: record.Id, Name: strings.TrimSpace(record.GetString("name"))})
	}
	for i := range fields {
		fields[i].Choices = byField[fields[i].ID]
	}
	return nil
}

// Coerce turns a value from a model, an archive or a client into what the
// field stores, or reports false when it does not fit the field's type. An
// option field takes an option id; a choice field takes the name of one of its
// choices and stores that choice's id.
func (f CustomField) Coerce(v any) (any, bool) {
	switch f.Type {
	case CustomFieldNumber:
		return coerceNumber(v)
	case CustomFieldDate:
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		date, ok := NormalizeDocumentDate(s)
		return date, ok && date != ""
	case CustomFieldOption:
		s, ok := v.(string)
		s = strings.TrimSpace(s)
		return s, ok && s != ""
	case CustomFieldChoice:
		s, _ := v.(string)
		for _, choice := range f.Choices {
			if strings.EqualFold(strings.TrimSpace(s), choice.Name) {
				return choice.ID, true
			}
		}
		return nil, false
	default:
		return coerceText(v)
	}
}

// ValueIn finds the field's value in a map keyed by field name, as a model or
// an archive writes it, matching the name ignoring case, and coerces it.
func (f CustomField) ValueIn(byName map[string]any) (any, bool) {
	raw, ok := byName[f.Name]
	if !ok {
		for key, v := range byName {
			if strings.EqualFold(strings.TrimSpace(key), f.Name) {
				raw, ok = v, true
				break
			}
		}
	}
	if !ok {
		return nil, false
	}
	return f.Coerce(raw)
}

func coerceNumber(v any) (any, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return nil, false
		}
		n = parsed
	default:
		return nil, false
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, false
	}
	return n, true
}

func coerceText(v any) (any, bool) {
	var s string
	switch x := v.(type) {
	case string:
		s = strings.TrimSpace(x)
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return nil, false
	}
	if runes := []rune(s); len(runes) > MaxCustomFieldValueRunes {
		s = string(runes[:MaxCustomFieldValueRunes])
	}
	return s, s != ""
}

// SyncFieldChoices makes want, in order, the field's choices: one with an id
// keeps it and takes its new name, one without is created, and every other
// choice is deleted, its values with it. Renamed choices pass through a
// temporary name first, because the unique name index would refuse a swap
// taken one row at a time. Run it in a transaction, so a failure leaves the
// deletes undone too.
func SyncFieldChoices(app core.App, fieldID string, want []FieldChoice) error {
	existing, err := app.FindRecordsByFilter(CustomFieldChoicesCollection, "field = {:field}", "", 0, 0,
		dbx.Params{"field": fieldID})
	if err != nil {
		return err
	}
	byID := make(map[string]*core.Record, len(existing))
	for _, record := range existing {
		byID[record.Id] = record
	}
	kept := map[string]bool{}
	for _, choice := range want {
		kept[choice.ID] = true
	}
	for _, record := range existing {
		if !kept[record.Id] {
			if err := app.Delete(record); err != nil {
				return err
			}
		}
	}
	var renamed []*core.Record
	var names []string
	for _, choice := range want {
		record := byID[choice.ID]
		if record == nil || record.GetString("name") == choice.Name {
			continue
		}
		record.Set("name", "~"+record.Id)
		if err := app.Save(record); err != nil {
			return err
		}
		renamed = append(renamed, record)
		names = append(names, choice.Name)
	}
	for i, record := range renamed {
		record.Set("name", names[i])
		if err := app.Save(record); err != nil {
			return err
		}
	}
	coll, err := app.FindCachedCollectionByNameOrId(CustomFieldChoicesCollection)
	if err != nil {
		return err
	}
	for _, choice := range want {
		if choice.ID != "" {
			continue
		}
		record := core.NewRecord(coll)
		record.Set("field", fieldID)
		record.Set("name", choice.Name)
		if err := app.Save(record); err != nil {
			return err
		}
	}
	return nil
}
