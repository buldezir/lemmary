package ngxapi

import (
	"testing"
)

func TestDefaultUiSettingsTreatsAcmeAsUnset(t *testing.T) {
	app := bootTestApp(t)

	app.Settings().Meta.AppName = "Acme"
	if got := defaultUiSettings(app)["app_title"]; got != "Lemmary" {
		t.Fatalf("Acme placeholder app_title = %v, want Lemmary", got)
	}

	app.Settings().Meta.AppName = ""
	if got := defaultUiSettings(app)["app_title"]; got != "Lemmary" {
		t.Fatalf("empty app_title = %v, want Lemmary", got)
	}

	app.Settings().Meta.AppName = "My Archive"
	if got := defaultUiSettings(app)["app_title"]; got != "My Archive" {
		t.Fatalf("custom app_title = %v, want My Archive", got)
	}
}
