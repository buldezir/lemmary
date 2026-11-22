package appapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
)

var errOptionNotFound = errors.New("option not found")

// refuseOptionInRules keeps a rule from pointing at a deleted option, as merge
// and prune do; a field's own deletion takes its options without asking.
func refuseOptionInRules(e *core.RecordRequestEvent) error {
	used, err := rules.OptionInUse(e.App, e.Record.GetString("user"), e.Record.Id)
	if err != nil {
		return err
	}
	if used {
		lang := i18n.FromRequest(e.Request)
		return e.BadRequestError(i18n.Of(lang, i18n.Errorf("%q is used by one of your rules; change or delete the rule first.", e.Record.GetString("name"))), nil)
	}
	return e.Next()
}

func handlePostOptionMerge(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		var body struct {
			Into string `json:"into"`
		}
		if err := e.BindBody(&body); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		moved, err := mergeOption(app, ownerID, e.Request.PathValue("id"), body.Into)
		if errors.Is(err, errOptionNotFound) {
			return writeError(e, http.StatusNotFound, "Option not found.")
		}
		if _, invalid := errors.AsType[*i18n.Error](err); invalid {
			return writeBadRequest(e, err)
		}
		if err != nil {
			app.Logger().Error("option merge failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Merging failed.")
		}
		return writeJSON(e, http.StatusOK, map[string]int{"documents": moved})
	}
}

func mergeOption(app core.App, ownerID, fromID, intoID string) (int, error) {
	from, err := ownOption(app, ownerID, fromID)
	if err != nil {
		return 0, err
	}
	into, err := ownOption(app, ownerID, intoID)
	if err != nil {
		return 0, err
	}
	if from.Id == into.Id || from.GetString("field") != into.GetString("field") {
		return 0, i18n.Errorf("Pick another entry of the same kind to merge into.")
	}
	field := from.GetString("field")
	moved := 0
	err = app.RunInTransaction(func(tx core.App) error {
		values, err := tx.FindAllRecords(models.CustomFieldValuesCollection, dbx.HashExp{"option": from.Id})
		if err != nil {
			return err
		}
		for _, value := range values {
			value.Set("option", into.Id)
			if err := tx.Save(value); err != nil {
				return err
			}
		}
		moved = len(values)
		if err := rules.RepointOption(tx, ownerID, field, from.Id, into.Id); err != nil {
			return err
		}
		return tx.Delete(from)
	})
	return moved, err
}

func ownOption(app core.App, ownerID, id string) (*core.Record, error) {
	option, err := app.FindRecordById(models.CustomFieldOptionsCollection, id)
	if err != nil || option.GetString("user") != ownerID {
		return nil, errOptionNotFound
	}
	return option, nil
}
