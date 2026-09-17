package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Two language-model bindings instead of four. extract_* stays as the general
// model (extraction, Ask AI, AI search, and Deep Research's bulk reads);
// research_* is the model that drives the Deep Research reasoning loop, empty
// meaning the general one. chat_*, search_* and search_helper_* go.
//
// A search binding that differed from extraction was the closest thing an
// install had to a research model, so it is carried over. The helper is not:
// it was the cheaper model for bulk work, and the general model does that now.
//
// Split into named functions so the test can run each twice: a managed
// instance re-runs every migration on every boot.
func init() {
	m.Register(collapseLLMBindings, restoreLLMBindings)
}

var legacyLLMBindingFields = []string{
	"chat_provider_id", "chat_model",
	"search_provider_id", "search_model",
	"search_helper_provider_id", "search_helper_model",
}

func collapseLLMBindings(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	added := false
	if settings.Fields.GetByName("research_provider_id") == nil {
		settings.Fields.Add(&core.TextField{Name: "research_provider_id", Max: 15})
		added = true
	}
	if settings.Fields.GetByName("research_model") == nil {
		settings.Fields.Add(&core.TextField{Name: "research_model", Max: 200})
		added = true
	}
	if added {
		if err := app.Save(settings); err != nil {
			return err
		}
	}

	if settings.Fields.GetByName("search_model") != nil {
		if err := carrySearchBindingToResearch(app); err != nil {
			return err
		}
	}

	dropped := false
	for _, name := range legacyLLMBindingFields {
		if settings.Fields.GetByName(name) != nil {
			settings.Fields.RemoveByName(name)
			dropped = true
		}
	}
	if !dropped {
		return nil
	}
	return app.Save(settings)
}

// carrySearchBindingToResearch runs while the old columns still exist. The
// singleton is seeded at boot, not by a migration, so a fresh install has no
// record here and nothing to carry.
func carrySearchBindingToResearch(app core.App) error {
	record, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil {
		return nil
	}
	general := record.GetString("extract_provider_id") + "|" + record.GetString("extract_model")
	search := record.GetString("search_provider_id") + "|" + record.GetString("search_model")
	if search == "|" || search == general {
		return nil
	}
	record.Set("research_provider_id", record.GetString("search_provider_id"))
	record.Set("research_model", record.GetString("search_model"))
	return app.Save(record)
}

func restoreLLMBindings(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	for _, name := range legacyLLMBindingFields {
		if settings.Fields.GetByName(name) != nil {
			continue
		}
		size := 200
		if len(name) > len("_provider_id") && name[len(name)-len("_provider_id"):] == "_provider_id" {
			size = 15
		}
		settings.Fields.Add(&core.TextField{Name: name, Max: size})
	}
	for _, name := range []string{"research_provider_id", "research_model"} {
		settings.Fields.RemoveByName(name)
	}
	return app.Save(settings)
}
