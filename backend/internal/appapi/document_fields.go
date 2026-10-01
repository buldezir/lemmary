package appapi

import (
	"errors"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/worker"
)

// saveFieldsWithDocument stores the values a document update carries under
// "fields", by field id, in the transaction that saves the document, so a
// refused update changes neither. A field left out keeps its value and null or
// "" clears one. An option field takes a name: the owner's option of that name
// is reused, or created, as extraction does, and created before the
// transaction for the reason fieldWrites in the worker gives; a document the
// update then refuses leaves at most an unused option, which prune removes.
func saveFieldsWithDocument(e *core.RecordRequestEvent) error {
	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	raw, ok := info.Body["fields"]
	if !ok {
		return e.Next()
	}
	lang := i18n.FromRequest(e.Request)
	body, ok := raw.(map[string]any)
	if !ok {
		return e.BadRequestError(i18n.T(lang, "Invalid request body."), nil)
	}
	writes, err := documentFieldWrites(e.App, e.Record, body)
	if _, invalid := errors.AsType[*i18n.Error](err); invalid {
		return e.BadRequestError(i18n.Of(lang, err), nil)
	}
	if err != nil {
		return err
	}
	return e.App.RunInTransaction(func(txApp core.App) error {
		if err := models.SaveFieldValues(txApp, e.Record, writes); err != nil {
			return err
		}
		e.App = txApp
		return e.Next()
	})
}

// documentFieldWrites checks every value before it turns a name into an
// option, so a body refused here creates none.
func documentFieldWrites(app core.App, document *core.Record, body map[string]any) ([]models.FieldWrite, error) {
	fields, err := documentFields(app)
	if err != nil {
		return nil, err
	}
	writes := make([]models.FieldWrite, 0, len(body))
	for id, raw := range body {
		field, ok := fields[id]
		if !ok {
			return nil, i18n.Errorf("unknown custom field %q", id)
		}
		value, err := clientFieldValue(field, raw)
		if err != nil {
			return nil, err
		}
		writes = append(writes, models.FieldWrite{Field: field, Value: value})
	}
	for i, w := range writes {
		if name, ok := w.Value.(string); ok && w.Field.Type == models.CustomFieldOption {
			id, _, err := worker.EnsureOption(app, w.Field.ID, document.GetString("user"), name)
			if err != nil {
				return nil, err
			}
			writes[i].Value = id
		}
	}
	return writes, nil
}

// documentFields is every field a document can hold, by id.
func documentFields(app core.App) (map[string]models.CustomField, error) {
	defined, err := models.LoadCustomFields(app)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]models.CustomField, len(defined)+len(models.PredefinedCustomFields))
	for _, field := range append(defined, models.PredefinedCustomFields...) {
		fields[field.ID] = field
	}
	return fields, nil
}

// clientFieldValue turns what a client sent into nil to clear, the coerced
// value, or an option's name.
func clientFieldValue(field models.CustomField, raw any) (any, error) {
	s, isString := raw.(string)
	if raw == nil || (isString && strings.TrimSpace(s) == "") {
		return nil, nil
	}
	if field.Type == models.CustomFieldOption {
		if !isString {
			return nil, i18n.Errorf("%s must be a name", field.Name)
		}
		return strings.TrimSpace(s), nil
	}
	value, ok := field.Coerce(raw)
	if !ok {
		return nil, i18n.Errorf("%s must be a %s", field.Name, field.Type)
	}
	return value, nil
}
