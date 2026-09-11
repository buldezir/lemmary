package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds extraction_rules: free text an admin appends to the built-in extraction
// prompt, for house conventions the fixed prompt cannot know ("treat Rechnung
// as the document type Invoice"). Empty, which is the default, leaves the
// prompt exactly as it was.
//
// Split into named functions so the test can run each twice: a managed
// instance re-runs every migration on every boot.
func init() {
	m.Register(addExtractionRulesField, dropExtractionRulesField)
}

func addExtractionRulesField(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	if settings.Fields.GetByName("extraction_rules") != nil {
		return nil
	}
	// 4000 characters: room for real house rules, and a bound on how much of
	// every extraction request is spent on them.
	settings.Fields.Add(&core.TextField{Name: "extraction_rules", Max: 4000})
	return app.Save(settings)
}

func dropExtractionRulesField(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	if settings.Fields.GetByName("extraction_rules") == nil {
		return nil
	}
	settings.Fields.RemoveByName("extraction_rules")
	return app.Save(settings)
}
