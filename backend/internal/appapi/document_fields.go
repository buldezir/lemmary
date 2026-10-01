package appapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/worker"
)

// handlePatchDocumentFields sets the values of the fields the body names, by
// field id, in one transaction; a field left out keeps its value and null or
// "" clears one. An option field takes a name: the owner's option of that name
// is reused, or created, as extraction does.
func handlePatchDocumentFields(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		document, err := app.FindRecordById("documents", e.Request.PathValue("documentId"))
		if err != nil || document.GetString("user") != e.Auth.Id {
			return writeError(e, http.StatusNotFound, "Document not found.")
		}
		var body map[string]any
		if err := e.BindBody(&body); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		fields, err := documentFields(app)
		if err != nil {
			app.Logger().Error("load custom fields failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to save the document.")
		}

		err = app.RunInTransaction(func(txApp core.App) error {
			for id, raw := range body {
				field, ok := fields[id]
				if !ok {
					return i18n.Errorf("unknown custom field %q", id)
				}
				value, err := clientFieldValue(txApp, document, field, raw)
				if err != nil {
					return err
				}
				if err := models.SaveFieldValue(txApp, document, field, value); err != nil {
					return err
				}
			}
			return nil
		})
		if _, invalid := errors.AsType[*i18n.Error](err); invalid {
			return writeBadRequest(e, err)
		}
		if err != nil {
			app.Logger().Error("save custom field values failed", "document", document.Id, "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to save the document.")
		}
		e.Response.WriteHeader(http.StatusNoContent)
		return nil
	}
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

// clientFieldValue turns what a client sent into what SaveFieldValue stores:
// nil to clear, the coerced value, or an option's id.
func clientFieldValue(app core.App, document *core.Record, field models.CustomField, raw any) (any, error) {
	if s, ok := raw.(string); raw == nil || (ok && strings.TrimSpace(s) == "") {
		return nil, nil
	}
	if field.Type == models.CustomFieldOption {
		name, ok := raw.(string)
		if !ok {
			return nil, i18n.Errorf("%s must be a name", field.Name)
		}
		id, _, err := worker.EnsureOption(app, field.ID, document.GetString("user"), name)
		return id, err
	}
	value, ok := field.Coerce(raw)
	if !ok {
		return nil, i18n.Errorf("%s must be a %s", field.Name, field.Type)
	}
	return value, nil
}
