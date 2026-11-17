package appapi

import (
	"errors"
	"maps"
	"slices"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
	"lemmary/backend/internal/worker"
)

// validateRule is the trust boundary for a rule: the collection only checks
// that the JSON is JSON. A save that touches neither the conditions, the
// actions nor the triggers -- a toggle, a move -- is let through, so a rule
// whose tag was deleted since can still be switched off.
func validateRule(e *core.RecordRequestEvent) error {
	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	if !e.Record.IsNew() && !slices.ContainsFunc([]string{"conditions", "actions", "triggers"}, func(key string) bool {
		_, ok := info.Body[key]
		return ok
	}) {
		return e.Next()
	}
	lang := i18n.FromRequest(e.Request)
	rule, err := rules.FromRecord(e.Record)
	if err != nil {
		return e.BadRequestError(i18n.T(lang, "Invalid request body."), nil)
	}
	cat, err := rules.LoadCatalog(e.App, e.Record.GetString("user"))
	if err != nil {
		return err
	}
	if err := rules.Validate(rule, cat); err != nil {
		if invalid, ok := errors.AsType[*i18n.Error](err); ok {
			return e.BadRequestError(i18n.Of(lang, invalid), nil)
		}
		return err
	}
	return e.Next()
}

type documentEditSnapshot struct {
	tags   []string
	values models.FieldValues
}

func snapshotDocumentEdit(app core.App, document *core.Record) (documentEditSnapshot, error) {
	values, err := models.LoadDocumentFieldValues(app, document.Id)
	if err != nil {
		return documentEditSnapshot{}, err
	}
	tags := slices.Clone(document.GetStringSlice("tags"))
	slices.Sort(tags)
	return documentEditSnapshot{tags: tags, values: values}, nil
}

// runRulesAfterEdit queues the edited rules when an update changed the
// document's tags or field values. Bound ahead of saveFieldsWithDocument, so
// its Next covers the field writes as well; and it keeps its own app, since
// that hook leaves the event on a transaction that has ended by then.
func runRulesAfterEdit(e *core.RecordRequestEvent) error {
	app := e.App
	before, err := snapshotDocumentEdit(app, e.Record.Original())
	if err != nil {
		return err
	}
	if err := e.Next(); err != nil {
		return err
	}
	after, err := snapshotDocumentEdit(app, e.Record)
	if err != nil {
		return err
	}
	if slices.Equal(before.tags, after.tags) && maps.Equal(before.values, after.values) {
		return nil
	}
	if err := worker.EnqueueEditedRules(app, e.Record); err != nil {
		// The edit itself is saved; failing the request now would only make
		// the client retry a write that already happened.
		app.Logger().Error("queue edited rules", "document", e.Record.Id, "error", err)
	}
	return nil
}
