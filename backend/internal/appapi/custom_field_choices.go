package appapi

import (
	"errors"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/models"
)

// saveChoicesWithField stores the choices a custom_fields create or update
// carries under "choices", [{id, name}] in order with no id for a new one, in
// the transaction that saves the field: a refused save changes none of them,
// and a new choice field never exists without its choices. A save without
// "choices" leaves them as they are. The field's rules have passed by the time
// this runs, so only an admin gets here.
func saveChoicesWithField(e *core.RecordRequestEvent) error {
	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	raw, ok := info.Body["choices"]
	if !ok {
		return e.Next()
	}
	lang := i18n.FromRequest(e.Request)
	want, err := requestedChoices(e.App, e.Record, raw)
	if _, invalid := errors.AsType[*i18n.Error](err); invalid {
		return e.BadRequestError(i18n.Of(lang, err), nil)
	}
	if err != nil {
		return err
	}
	return e.App.RunInTransaction(func(txApp core.App) error {
		e.App = txApp
		if err := e.Next(); err != nil {
			return err
		}
		if e.Record.GetString("type") != models.CustomFieldChoice {
			return nil
		}
		return models.SyncFieldChoices(txApp, e.Record.Id, want)
	})
}

// requestedChoices checks the whole list before anything is written: names
// trimmed, none blank or repeated ignoring case, and every id one of this
// field's own choices.
func requestedChoices(app core.App, field *core.Record, raw any) ([]models.FieldChoice, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, i18n.Errorf("Invalid request body.")
	}
	name := strings.TrimSpace(field.GetString("name"))
	if field.GetString("type") != models.CustomFieldChoice {
		if len(list) > 0 {
			return nil, i18n.Errorf("only a choice field has choices")
		}
		return nil, nil
	}
	if len(list) == 0 {
		return nil, i18n.Errorf("%s needs at least one choice", name)
	}
	own := map[string]bool{}
	if field.Id != "" {
		records, err := app.FindAllRecords(models.CustomFieldChoicesCollection, dbx.HashExp{"field": field.Id})
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			own[record.Id] = true
		}
	}
	want := make([]models.FieldChoice, 0, len(list))
	seen := map[string]bool{}
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, i18n.Errorf("Invalid request body.")
		}
		id, _ := entry["id"].(string)
		choiceName, _ := entry["name"].(string)
		choiceName = strings.TrimSpace(choiceName)
		if choiceName == "" {
			return nil, i18n.Errorf("a choice needs a name")
		}
		if id != "" && !own[id] {
			return nil, i18n.Errorf("unknown choice %q", id)
		}
		key := strings.ToLower(choiceName)
		if seen[key] {
			return nil, i18n.Errorf("the choice %q is listed twice", choiceName)
		}
		seen[key] = true
		want = append(want, models.FieldChoice{ID: id, Name: choiceName})
	}
	return want, nil
}
