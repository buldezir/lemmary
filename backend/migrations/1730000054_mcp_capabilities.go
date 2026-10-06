package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds mcp_capabilities: the write tools (config.MCPCapabilities) the MCP
// endpoint offers an agent. Empty, the default, keeps it read-only.
//
// Split into named functions so the test can run the add twice: a managed
// instance re-runs every migration on every boot.
func init() {
	m.Register(addMCPCapabilitiesField, dropMCPCapabilitiesField)
}

func addMCPCapabilitiesField(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	if settings.Fields.GetByName("mcp_capabilities") != nil {
		return nil
	}
	settings.Fields.Add(&core.SelectField{
		Name:      "mcp_capabilities",
		Values:    []string{"edit", "reprocess", "upload", "delete", "tags"},
		MaxSelect: 5,
	})
	return app.Save(settings)
}

func dropMCPCapabilitiesField(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	settings.Fields.RemoveByName("mcp_capabilities")
	return app.Save(settings)
}
