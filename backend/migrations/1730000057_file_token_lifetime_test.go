package migrations

import "testing"

func TestUsersFileTokensLastFifteenMinutes(t *testing.T) {
	app := bootMigratedApp(t)

	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	if got := users.FileToken.Duration; got != 15*60 {
		t.Fatalf("file token duration = %ds, want %ds", got, 15*60)
	}
}
