package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}

		ownerRule := "user = @request.auth.id"

		accounts := core.NewBaseCollection("mail_accounts")
		accounts.ListRule = types.Pointer(ownerRule)
		accounts.ViewRule = types.Pointer(ownerRule)
		accounts.CreateRule = nil
		accounts.UpdateRule = nil
		accounts.DeleteRule = nil
		accounts.Fields.Add(
			&core.RelationField{
				Name:         "user",
				Required:     true,
				CollectionId: users.Id,
				MaxSelect:    1,
			},
			&core.TextField{Name: "email", Required: true, Max: 255},
			&core.TextField{Name: "refresh_token", Required: true, Max: 2000},
			&core.BoolField{Name: "enabled"},
			&core.BoolField{Name: "cron_enabled"},
			&core.NumberField{Name: "cron_lookback_days", OnlyInt: true},
			&core.SelectField{
				Name:     "triage_mode",
				Required: true,
				Values:   []string{"simple", "deep"},
			},
			&core.DateField{Name: "last_synced_at"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		accounts.AddIndex("idx_mail_accounts_user_email", true, "user, email", "")
		if err := app.Save(accounts); err != nil {
			return err
		}

		scans := core.NewBaseCollection("mail_scans")
		scans.ListRule = types.Pointer(ownerRule)
		scans.ViewRule = types.Pointer(ownerRule)
		scans.CreateRule = nil
		scans.UpdateRule = nil
		scans.DeleteRule = nil
		scans.Fields.Add(
			&core.RelationField{
				Name:         "user",
				Required:     true,
				CollectionId: users.Id,
				MaxSelect:    1,
			},
			&core.RelationField{
				Name:         "account",
				Required:     true,
				CollectionId: accounts.Id,
				MaxSelect:    1,
			},
			&core.SelectField{
				Name:     "trigger",
				Required: true,
				Values:   []string{"manual", "cron"},
			},
			&core.SelectField{
				Name:     "mode",
				Required: true,
				Values:   []string{"simple", "deep"},
			},
			&core.TextField{Name: "date_from", Required: true, Max: 32},
			&core.TextField{Name: "date_to", Required: true, Max: 32},
			&core.SelectField{
				Name:     "status",
				Required: true,
				Values:   []string{"pending", "running", "completed", "failed"},
			},
			&core.JSONField{Name: "progress_json"},
			&core.TextField{Name: "error", Max: 5000},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(scans); err != nil {
			return err
		}

		imports := core.NewBaseCollection("mail_imports")
		imports.ListRule = types.Pointer(ownerRule)
		imports.ViewRule = types.Pointer(ownerRule)
		imports.CreateRule = nil
		imports.UpdateRule = nil
		imports.DeleteRule = nil
		imports.Fields.Add(
			&core.RelationField{
				Name:         "user",
				Required:     true,
				CollectionId: users.Id,
				MaxSelect:    1,
			},
			&core.RelationField{
				Name:         "account",
				Required:     true,
				CollectionId: accounts.Id,
				MaxSelect:    1,
			},
			&core.TextField{Name: "gmail_message_id", Required: true, Max: 200},
			&core.TextField{Name: "attachment_id", Required: true, Max: 500},
			&core.TextField{Name: "filename", Max: 500},
			&core.RelationField{
				Name:         "document",
				CollectionId: documents.Id,
				MaxSelect:    1,
			},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		imports.AddIndex("idx_mail_imports_dedup", true, "account, gmail_message_id, attachment_id", "")
		return app.Save(imports)
	}, func(app core.App) error {
		for _, name := range []string{"mail_imports", "mail_scans", "mail_accounts"} {
			collection, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				continue
			}
			if err := app.Delete(collection); err != nil {
				return err
			}
		}
		return nil
	})
}
