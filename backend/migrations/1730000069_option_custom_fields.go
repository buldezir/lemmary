package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
)

// Lets an admin add option fields of their own, whose values are each user's
// vocabulary as Correspondent's are. The two predefined fields stay nobody's
// to change, now by id rather than by type.
func init() {
	m.Register(allowOptionCustomFields, disallowOptionCustomFields)
}

func customFieldRules(app core.App, create, update, del string) error {
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		return err
	}
	admin := "@request.auth." + pairedAdminField + " = true"
	coll.CreateRule = new(admin + create)
	coll.UpdateRule = new(admin + update + " && @request.body.type:changed = false")
	coll.DeleteRule = new(admin + del)
	return app.Save(coll)
}

func allowOptionCustomFields(app core.App) error {
	notPredefined := " && id != '" + models.CorrespondentFieldID + "' && id != '" + models.DocumentTypeFieldID + "'"
	return customFieldRules(app, "", notPredefined, notPredefined)
}

func disallowOptionCustomFields(app core.App) error {
	fields, err := app.FindAllRecords(models.CustomFieldsCollection, dbx.HashExp{"type": models.CustomFieldOption})
	if err != nil {
		return err
	}
	for _, field := range fields {
		if models.IsPredefinedField(field.Id) {
			continue
		}
		if err := app.Delete(field); err != nil {
			return err
		}
	}
	notOption := " && type != 'option'"
	return customFieldRules(app, " && @request.body.type != 'option'", notOption, notOption)
}
