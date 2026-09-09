package chat_test

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chat"
	// Registers the migrations that create users and documents, which
	// chat_sessions relates to. Importing them is also why this file is an
	// external test package: internal/chat cannot import migrations, which
	// import it back to define the collections.
	_ "lemmary/backend/migrations"
)

func bootAppForStore(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	if err := app.RunAppMigrations(); err != nil {
		t.Fatalf("run app migrations: %v", err)
	}
	return app
}

func makeUser(t *testing.T, app core.App, email string) string {
	t.Helper()
	users, err := app.FindCollectionByNameOrId(chat.UsersCollectionName)
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	user := core.NewRecord(users)
	user.Set("email", email)
	user.SetPassword("test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return user.Id
}

// The binding is fixed for a conversation, so it has to survive the write and
// come back out: without the round trip, every turn after the first would
// silently fall back to the model in Settings.
func TestCreateSessionStoresAndReturnsItsBinding(t *testing.T) {
	app := bootAppForStore(t)
	userID := makeUser(t, app, "binding@example.test")

	want := aiprovider.Binding{ProviderID: "provider1234567", Model: "gpt-6-astra"}
	session, err := chat.CreateSession(app, chat.NewSession{
		UserID:       userID,
		Kind:         chat.KindSearch,
		Mode:         chat.ModeResearch,
		Binding:      want,
		FirstMessage: "how much did I spend on the car in 2024?",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Re-read rather than trusting the in-memory record: the columns are what a
	// later turn reads the binding back out of.
	stored, err := app.FindRecordById(chat.SessionsCollection, session.Id)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if got := chat.BindingOf(stored); got != want {
		t.Fatalf("BindingOf() = %+v, want %+v", got, want)
	}
	// Sent to the client too, so reopening the chat restores the picker.
	if info := chat.ToSessionInfo(stored); info.Provider != want.ProviderID || info.Model != want.Model {
		t.Fatalf("SessionInfo binding = %q/%q, want %q/%q",
			info.Provider, info.Model, want.ProviderID, want.Model)
	}
}

// A chat opened on the configured model must store nothing, so it reads back as
// "use Settings" -- which is every session that existed before overrides did.
func TestCreateSessionLeavesTheBindingUnsetWhenThereIsNone(t *testing.T) {
	app := bootAppForStore(t)
	userID := makeUser(t, app, "nobinding@example.test")

	session, err := chat.CreateSession(app, chat.NewSession{
		UserID:       userID,
		Kind:         chat.KindSearch,
		Mode:         chat.ModeSearch,
		FirstMessage: "plumber invoice",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := chat.BindingOf(session); !got.Empty() {
		t.Fatalf("BindingOf() = %+v, want empty", got)
	}
	if info := chat.ToSessionInfo(session); info.Provider != "" || info.Model != "" {
		t.Fatalf("SessionInfo carried a binding: %q/%q", info.Provider, info.Model)
	}
}

// A model with no provider is refused by the API, so it must not become a
// stored half-binding either -- BindingOf would hand it back and the next
// turn would be refused for a choice nobody made.
func TestCreateSessionDropsAModelWithNoProvider(t *testing.T) {
	app := bootAppForStore(t)
	userID := makeUser(t, app, "halfbinding@example.test")

	session, err := chat.CreateSession(app, chat.NewSession{
		UserID:       userID,
		Kind:         chat.KindSearch,
		Mode:         chat.ModeSearch,
		Binding:      aiprovider.Binding{Model: "gpt-6-astra"},
		FirstMessage: "plumber invoice",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := chat.BindingOf(session); !got.Empty() {
		t.Fatalf("BindingOf() = %+v, want empty", got)
	}
}

func TestBindingOfNilRecord(t *testing.T) {
	t.Parallel()
	if got := chat.BindingOf(nil); !got.Empty() {
		t.Fatalf("BindingOf(nil) = %+v, want empty", got)
	}
}
