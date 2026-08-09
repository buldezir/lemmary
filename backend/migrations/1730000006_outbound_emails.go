package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// Locked down for regular users; superusers bypass rules.
		// Used as the sendmail replacement when SMTP is not configured.
		mails := core.NewBaseCollection("outbound_emails")
		mails.Fields.Add(
			&core.TextField{Name: "from_address", Required: true, Max: 255},
			&core.TextField{Name: "from_name", Max: 255},
			&core.JSONField{Name: "to"},
			&core.JSONField{Name: "cc"},
			&core.JSONField{Name: "bcc"},
			&core.TextField{Name: "subject", Max: 1000},
			&core.TextField{Name: "html", Max: 500000},
			&core.TextField{Name: "text", Max: 500000},
			&core.JSONField{Name: "headers"},
			&core.JSONField{Name: "attachment_names"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		mails.AddIndex("idx_outbound_emails_created", false, "created", "")
		return app.Save(mails)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("outbound_emails")
		if err != nil {
			return nil
		}
		return app.Delete(collection)
	})
}
