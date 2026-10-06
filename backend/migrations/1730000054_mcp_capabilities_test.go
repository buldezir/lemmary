package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

func TestMCPCapabilitiesReachConfig(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	settings.Set("mcp_capabilities", []string{"edit", "tags"})
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}

	cfg, err := config.Load(app)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !slices.Equal(cfg.MCPCapabilities, []string{"edit", "tags"}) {
		t.Fatalf("Config.MCPCapabilities = %v", cfg.MCPCapabilities)
	}
}

func TestMCPCapabilitiesMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	if err := addMCPCapabilitiesField(app); err != nil {
		t.Fatalf("addMCPCapabilitiesField on an already-migrated app: %v", err)
	}
	if err := addMCPCapabilitiesField(app); err != nil {
		t.Fatalf("addMCPCapabilitiesField a second time: %v", err)
	}
}
