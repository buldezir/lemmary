package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

func TestResearchBindingRoundTripsAndFallsBackToGeneral(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	for _, name := range legacyLLMBindingFields {
		if collection.Fields.GetByName(name) != nil {
			t.Fatalf("%s should have been dropped", name)
		}
	}

	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	settings.Set("extract_model", "general-model")
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}

	cfg, err := config.Load(app)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, model := cfg.ResearchBinding(); model != "general-model" || cfg.ResearchModel != "" {
		t.Fatalf("research should fall back to the general model and stay stored as empty, got %q / %q", model, cfg.ResearchModel)
	}

	settings.Set("research_model", "big-model")
	if err := app.Save(settings); err != nil {
		t.Fatalf("save research_model: %v", err)
	}
	cfg, err = config.Load(app)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.ResearchModel != "big-model" || cfg.ExtractModel != "general-model" {
		t.Fatalf("Config = extract %q research %q", cfg.ExtractModel, cfg.ResearchModel)
	}
}

// An install that had bound search apart from extraction meant it: that pair
// becomes the research binding. One that had them equal gets an empty research
// binding, which reads as "same as general" in Settings.
func TestCollapseCarriesADistinctSearchBindingIntoResearch(t *testing.T) {
	for _, tc := range []struct {
		name         string
		searchModel  string
		wantResearch string
	}{
		{"distinct search model", "search-model", "search-model"},
		{"same as extraction", "general-model", ""},
		{"unbound search", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := bootMigratedApp(t)
			if err := restoreLLMBindings(app); err != nil {
				t.Fatalf("restore legacy columns: %v", err)
			}
			collection, err := app.FindCollectionByNameOrId("app_settings")
			if err != nil {
				t.Fatalf("app_settings collection: %v", err)
			}
			settings := core.NewRecord(collection)
			settings.Id = "appsettings0001"
			settings.MarkAsNew()
			settings.Set("extract_provider_id", "prov")
			settings.Set("extract_model", "general-model")
			if tc.searchModel != "" {
				settings.Set("search_provider_id", "prov")
				settings.Set("search_model", tc.searchModel)
			}
			settings.Set("search_helper_model", "cheap-model")
			if err := app.Save(settings); err != nil {
				t.Fatalf("save legacy settings: %v", err)
			}

			if err := collapseLLMBindings(app); err != nil {
				t.Fatalf("collapse: %v", err)
			}
			if err := collapseLLMBindings(app); err != nil {
				t.Fatalf("collapse a second time: %v", err)
			}

			cfg, err := config.Load(app)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			reloaded, err := app.FindRecordById("app_settings", "appsettings0001")
			if err != nil {
				t.Fatalf("reload settings: %v", err)
			}
			if got := reloaded.GetString("research_model"); got != tc.wantResearch {
				t.Fatalf("research_model = %q, want %q", got, tc.wantResearch)
			}
			// Resolved, research is never empty while the general model is bound.
			if _, model := cfg.ResearchBinding(); model == "" {
				t.Fatalf("resolved research model is empty")
			}
		})
	}
}
