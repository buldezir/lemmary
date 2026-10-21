package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds the processing schedule: a daily window jobs may start in
// (config.ProcessingWindow) and a ping that holds them while the AI server is
// down. All off by default, which is processing around the clock.
//
// Split into named functions so the test can run the add twice: a managed
// instance re-runs every migration on every boot.
func init() {
	m.Register(addProcessingScheduleFields, dropProcessingScheduleFields)
}

var processingScheduleFields = []core.Field{
	&core.BoolField{Name: "processing_window_enabled"},
	&core.TextField{Name: "processing_window_start", Max: 5},
	&core.TextField{Name: "processing_window_end", Max: 5},
	&core.TextField{Name: "processing_timezone", Max: 64},
	&core.BoolField{Name: "processing_ping_enabled"},
}

func addProcessingScheduleFields(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	for _, field := range processingScheduleFields {
		if settings.Fields.GetByName(field.GetName()) == nil {
			settings.Fields.Add(field)
		}
	}
	return app.Save(settings)
}

func dropProcessingScheduleFields(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	for _, field := range processingScheduleFields {
		settings.Fields.RemoveByName(field.GetName())
	}
	return app.Save(settings)
}
