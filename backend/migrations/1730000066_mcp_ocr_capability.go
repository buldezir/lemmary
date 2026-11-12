package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds "ocr" to app_settings.mcp_capabilities: the edit_document_text tool.
func init() {
	m.Register(func(app core.App) error {
		return setMCPCapabilityValues(app, []string{"edit", "ocr", "reprocess", "upload", "delete", "tags"})
	}, func(app core.App) error {
		records, err := app.FindAllRecords("app_settings")
		if err != nil {
			return err
		}
		for _, record := range records {
			record.Set("mcp_capabilities-", "ocr")
			if err := app.Save(record); err != nil {
				return err
			}
		}
		return setMCPCapabilityValues(app, []string{"edit", "reprocess", "upload", "delete", "tags"})
	})
}

func setMCPCapabilityValues(app core.App, values []string) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	if field, ok := settings.Fields.GetByName("mcp_capabilities").(*core.SelectField); ok {
		field.Values = values
		field.MaxSelect = len(values)
	}
	return app.Save(settings)
}
