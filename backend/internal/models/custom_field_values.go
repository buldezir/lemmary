package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// FieldValue is one stored value. Only the part matching Type is set.
type FieldValue struct {
	Type       string
	Text       string
	Number     float64
	Date       string // YYYY-MM-DD
	Option     string // custom_field_options id
	OptionName string
}

// Value is what the field holds as an archive or the search index carries it:
// an option reads as its name.
func (v FieldValue) Value() any {
	switch v.Type {
	case CustomFieldNumber:
		return v.Number
	case CustomFieldDate:
		return v.Date
	case CustomFieldOption:
		return v.OptionName
	}
	return v.Text
}

// FieldValues is a document's values keyed by field id.
type FieldValues map[string]FieldValue

// OptionID is the option an option field points at, or "".
func (v FieldValues) OptionID(fieldID string) string { return v[fieldID].Option }

// OptionName is the name of the option an option field points at, or "".
func (v FieldValues) OptionName(fieldID string) string { return v[fieldID].OptionName }

const fieldValuesChunk = 500

// LoadFieldValues reads the values of every given document in one query per
// 500 documents, options resolved to their names. A document without values is
// absent from the map.
func LoadFieldValues(app core.App, docIDs ...string) (map[string]FieldValues, error) {
	out := make(map[string]FieldValues, len(docIDs))
	for start := 0; start < len(docIDs); start += fieldValuesChunk {
		chunk := docIDs[start:min(start+fieldValuesChunk, len(docIDs))]
		params := dbx.Params{}
		names := make([]string, len(chunk))
		for i, id := range chunk {
			key := fmt.Sprintf("d%d", i)
			params[key] = id
			names[i] = "{:" + key + "}"
		}
		if err := loadFieldValuesInto(app, out, "v.document IN ("+strings.Join(names, ", ")+")", params); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// LoadAllFieldValues is every document's values, for a caller that walks the
// whole archive.
func LoadAllFieldValues(app core.App) (map[string]FieldValues, error) {
	out := map[string]FieldValues{}
	return out, loadFieldValuesInto(app, out, "1 = 1", dbx.Params{})
}

func loadFieldValuesInto(app core.App, out map[string]FieldValues, where string, params dbx.Params) error {
	var rows []struct {
		Document   string  `db:"document"`
		Field      string  `db:"field"`
		Type       string  `db:"type"`
		Text       string  `db:"text"`
		Number     float64 `db:"number"`
		Date       string  `db:"date"`
		Option     string  `db:"option"`
		OptionName string  `db:"option_name"`
	}
	query := `SELECT v.document, v.field, f.type, v.text, v.number, v.date, v.option,
		COALESCE(o.name, '') AS option_name
		FROM ` + CustomFieldValuesCollection + ` v
		JOIN ` + CustomFieldsCollection + ` f ON f.id = v.field
		LEFT JOIN ` + CustomFieldOptionsCollection + ` o ON o.id = v.option
		WHERE ` + where
	if err := app.DB().NewQuery(query).Bind(params).All(&rows); err != nil {
		return err
	}
	for _, row := range rows {
		if out[row.Document] == nil {
			out[row.Document] = FieldValues{}
		}
		out[row.Document][row.Field] = FieldValue{
			Type:       row.Type,
			Text:       row.Text,
			Number:     row.Number,
			Date:       truncateDate(row.Date),
			Option:     row.Option,
			OptionName: row.OptionName,
		}
	}
	return nil
}

// LoadDocumentFieldValues is LoadFieldValues for one document; never nil.
func LoadDocumentFieldValues(app core.App, docID string) (FieldValues, error) {
	values, err := LoadFieldValues(app, docID)
	if err != nil {
		return nil, err
	}
	if values[docID] == nil {
		return FieldValues{}, nil
	}
	return values[docID], nil
}

func truncateDate(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// SaveFieldValue stores an already coerced value (see CustomField.Coerce) as
// document's value for field, or removes it when value is nil. An option must
// be the document owner's and the field's: the values collection has no write
// rules, so this is the only check there is.
func SaveFieldValue(app core.App, document *core.Record, field CustomField, value any) error {
	existing, err := app.FindFirstRecordByFilter(CustomFieldValuesCollection,
		"document = {:document} && field = {:field}", dbx.Params{"document": document.Id, "field": field.ID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if value == nil {
		if existing == nil {
			return nil
		}
		return app.Delete(existing)
	}

	record := existing
	if record == nil {
		coll, err := app.FindCachedCollectionByNameOrId(CustomFieldValuesCollection)
		if err != nil {
			return err
		}
		record = core.NewRecord(coll)
		record.Set("document", document.Id)
		record.Set("field", field.ID)
	}
	for _, column := range []string{"text", "number", "date", "option"} {
		record.Set(column, nil)
	}
	switch field.Type {
	case CustomFieldNumber:
		record.Set("number", value)
	case CustomFieldDate:
		record.Set("date", value)
	case CustomFieldOption:
		id, _ := value.(string)
		if err := requireOwnOption(app, document, field, id); err != nil {
			return err
		}
		record.Set("option", id)
	default:
		record.Set("text", value)
	}
	return app.Save(record)
}

// FieldWrite is one value for SaveFieldValues; a nil Value clears the field.
type FieldWrite struct {
	Field CustomField
	Value any
}

// SaveFieldValues is SaveFieldValue for each write, in order. Run it in the
// transaction that saves the document, so a refused document keeps its values.
func SaveFieldValues(app core.App, document *core.Record, writes []FieldWrite) error {
	for _, w := range writes {
		if err := SaveFieldValue(app, document, w.Field, w.Value); err != nil {
			return err
		}
	}
	return nil
}

func requireOwnOption(app core.App, document *core.Record, field CustomField, id string) error {
	option, err := app.FindRecordById(CustomFieldOptionsCollection, id)
	if err != nil {
		return fmt.Errorf("unknown option %q", id)
	}
	if option.GetString("field") != field.ID || option.GetString("user") != document.GetString("user") {
		return fmt.Errorf("option %q is not the document owner's %s", id, field.Name)
	}
	return nil
}
