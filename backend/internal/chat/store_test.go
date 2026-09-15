package chat_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chat"
	// Registers the migrations that create users and documents. Importing them
	// is why this file is an external test package: internal/chat cannot import
	// migrations, which import it back.
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

// The binding is fixed for a conversation, so it has to survive the write:
// otherwise every turn after the first falls back to the model in Settings.
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

	// Re-read rather than trusting the in-memory record.
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

// A chat opened on the configured model must store nothing, so it reads back
// as "use Settings".
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
// stored half-binding either.
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

// Recovery identifies a request by its client-generated run id. Both records
// in the pair carry it, so the stored transcript is self-contained.
func TestAppendTurnStoresAndReturnsRunID(t *testing.T) {
	app := bootAppForStore(t)
	userID := makeUser(t, app, "run-id@example.test")
	session, err := chat.CreateSession(app, chat.NewSession{
		UserID:       userID,
		Kind:         chat.KindSearch,
		Mode:         chat.ModeSearch,
		FirstMessage: "same question",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := chat.AppendTurn(app, userID, session.Id, chat.Turn{
		UserContent:      "same question",
		AssistantContent: "this run's answer",
		RunID:            "run-current",
	}); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	records, err := chat.ListMessages(app, session.Id, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d messages, want 2", len(records))
	}
	for _, record := range records {
		if got := chat.ToMessageInfo(record).RunID; got != "run-current" {
			t.Fatalf("message run id = %q, want run-current", got)
		}
	}
}

// Research progress is stored on the assistant row. The user half stays empty,
// and History still sees only role+content.
func TestAppendTurnStoresStepsAndIncompleteOnTheAssistant(t *testing.T) {
	app := bootAppForStore(t)
	userID := makeUser(t, app, "steps@example.test")
	session, err := chat.CreateSession(app, chat.NewSession{
		UserID:       userID,
		Kind:         chat.KindSearch,
		Mode:         chat.ModeResearch,
		FirstMessage: "how much?",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	steps := []chat.StoredStep{
		{Kind: "search", Status: "start", Query: "plumbing"},
		{Kind: "search", Status: "done", Query: "plumbing", Count: 2},
	}
	if _, err := chat.AppendTurn(app, userID, session.Id, chat.Turn{
		UserContent:      "how much?",
		AssistantContent: "€412.",
		Steps:            steps,
		Incomplete:       true,
	}); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	records, err := chat.ListMessages(app, session.Id, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d messages, want 2", len(records))
	}
	user := chat.ToMessageInfo(records[0])
	if len(user.Steps) != 0 || user.Incomplete {
		t.Fatalf("user message carried research extras: %+v", user)
	}
	assistant := chat.ToMessageInfo(records[1])
	if !assistant.Incomplete {
		t.Fatal("assistant incomplete = false, want true")
	}
	if len(assistant.Steps) != 2 || assistant.Steps[0].Query != "plumbing" {
		t.Fatalf("assistant steps = %+v", assistant.Steps)
	}

	history, err := chat.History(app, session.Id)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 || history[1].Content != "€412." {
		t.Fatalf("History = %+v", history)
	}
}

// A fork is the whole conversation again under a new id: the transcript it
// branches from, the fields a turn is refused for contradicting, and nothing
// the source can see afterwards.
func TestForkSessionCopiesTheTranscriptAndLeavesTheSourceAlone(t *testing.T) {
	app := bootAppForStore(t)
	userID := makeUser(t, app, "fork@example.test")

	binding := aiprovider.Binding{ProviderID: "provider1234567", Model: "gpt-6-astra"}
	source, err := chat.CreateSession(app, chat.NewSession{
		UserID:       userID,
		Kind:         chat.KindSearch,
		Mode:         chat.ModeResearch,
		Binding:      binding,
		FirstMessage: "how much did I spend on the car in 2024?",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := chat.AppendTurn(app, userID, source.Id, chat.Turn{
		UserContent:      "how much did I spend on the car in 2024?",
		AssistantContent: "€412.",
		RunID:            "run-source",
		Documents:        []ai.DocumentHit{{ID: "doc1", Title: "Garage invoice"}},
		Steps:            []chat.StoredStep{{Kind: "search", Status: "done", Query: "car"}},
		Incomplete:       true,
		Mode:             chat.ModeResearch,
	}); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}

	fork, err := chat.ForkSession(app, userID, source, "")
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if fork.Id == source.Id {
		t.Fatal("ForkSession returned the source session")
	}

	stored, err := app.FindRecordById(chat.SessionsCollection, fork.Id)
	if err != nil {
		t.Fatalf("reload fork: %v", err)
	}
	info := chat.ToSessionInfo(stored)
	if info.Kind != string(chat.KindSearch) || info.Mode != chat.ModeResearch {
		t.Fatalf("fork kind/mode = %q/%q", info.Kind, info.Mode)
	}
	// Both are fixed for a conversation's lifetime, so a copy that lost them
	// would answer the rest of the transcript with another model.
	if got := chat.BindingOf(stored); got != binding {
		t.Fatalf("fork binding = %+v, want %+v", got, binding)
	}
	if !strings.HasPrefix(info.Title, chat.ForkMark) {
		t.Fatalf("fork title = %q, want it marked", info.Title)
	}
	if info.MessageCount != 2 {
		t.Fatalf("fork message_count = %d, want 2", info.MessageCount)
	}

	messages, err := chat.ListMessages(app, fork.Id, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("fork holds %d messages, want 2", len(messages))
	}
	user := chat.ToMessageInfo(messages[0])
	assistant := chat.ToMessageInfo(messages[1])
	if user.Seq != 1 || assistant.Seq != 2 {
		t.Fatalf("fork seqs = %d/%d, want 1/2", user.Seq, assistant.Seq)
	}
	if user.Content != "how much did I spend on the car in 2024?" || assistant.Content != "€412." {
		t.Fatalf("fork transcript = %q / %q", user.Content, assistant.Content)
	}
	// The evidence and the trail are a snapshot of what the answer was built
	// from, so they travel with it rather than being re-derived.
	if len(assistant.Documents) != 1 || assistant.Documents[0].ID != "doc1" {
		t.Fatalf("fork hits = %+v", assistant.Documents)
	}
	if len(assistant.Steps) != 1 || assistant.Steps[0].Query != "car" {
		t.Fatalf("fork steps = %+v", assistant.Steps)
	}
	if !assistant.Incomplete {
		t.Fatal("fork assistant incomplete = false, want true")
	}
	// A copied run id would let a recovery poll collect an answer this session
	// never produced.
	if user.RunID != "" || assistant.RunID != "" {
		t.Fatalf("fork carried run ids: %q/%q", user.RunID, assistant.RunID)
	}

	// And the chat it came from is exactly as it was.
	sourceStored, err := app.FindRecordById(chat.SessionsCollection, source.Id)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if got := chat.ToSessionInfo(sourceStored); got.MessageCount != 2 || strings.HasPrefix(got.Title, chat.ForkMark) {
		t.Fatalf("source changed: %+v", got)
	}
	sourceMessages, err := chat.ListMessages(app, source.Id, 0)
	if err != nil {
		t.Fatalf("ListMessages(source): %v", err)
	}
	if len(sourceMessages) != 2 {
		t.Fatalf("source holds %d messages, want 2", len(sourceMessages))
	}
	if got := chat.ToMessageInfo(sourceMessages[0]).RunID; got != "run-source" {
		t.Fatalf("source run id = %q, want run-source", got)
	}
}

// Branching off an answer the conversation has since moved past: the copy ends
// where it was asked to, and what came after it is what the fork exists to
// leave behind.
func TestForkSessionCutsTheTranscriptAtUpto(t *testing.T) {
	app := bootAppForStore(t)
	userID := makeUser(t, app, "fork-upto@example.test")

	source, err := chat.CreateSession(app, chat.NewSession{
		UserID:       userID,
		Kind:         chat.KindSearch,
		Mode:         chat.ModeResearch,
		FirstMessage: "how much did I spend on the car in 2024?",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, turn := range []chat.Turn{
		{UserContent: "how much did I spend on the car in 2024?", AssistantContent: "€412."},
		{UserContent: "and on the boiler?", AssistantContent: "€180."},
	} {
		turn.Mode = chat.ModeResearch
		if _, err := chat.AppendTurn(app, userID, source.Id, turn); err != nil {
			t.Fatalf("AppendTurn: %v", err)
		}
	}

	messages, err := chat.ListMessages(app, source.Id, 0)
	if err != nil {
		t.Fatalf("ListMessages(source): %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("source holds %d messages, want 4", len(messages))
	}

	fork, err := chat.ForkSession(app, userID, source, messages[1].Id)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	forked, err := chat.ListMessages(app, fork.Id, 0)
	if err != nil {
		t.Fatalf("ListMessages(fork): %v", err)
	}
	if len(forked) != 2 {
		t.Fatalf("fork holds %d messages, want the first pair only", len(forked))
	}
	first := chat.ToMessageInfo(forked[0])
	second := chat.ToMessageInfo(forked[1])
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("fork seqs = %d/%d, want 1/2", first.Seq, second.Seq)
	}
	if second.Content != "€412." {
		t.Fatalf("fork ends on %q, want the answer it branched from", second.Content)
	}
	if info := chat.ToSessionInfo(fork); info.MessageCount != 2 {
		t.Fatalf("fork message_count = %d, want 2", info.MessageCount)
	}

	// An anchor the source does not hold means the transcript moved under the
	// client, and a whole copy is not what it asked for.
	if _, err := chat.ForkSession(app, userID, source, "nosuchmessage00"); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("ForkSession with an unknown upto = %v, want ErrNotFound", err)
	}
	if total, err := chat.CountSessions(app, userID); err != nil || total != 2 {
		t.Fatalf("CountSessions() = %d, %v; want 2 and no error", total, err)
	}
}

// The cap is what stops an account turning the sidebar into an unbounded table,
// and a fork is a new row like any other.
func TestForkSessionRefusesPastTheSessionCap(t *testing.T) {
	app := bootAppForStore(t)
	userID := makeUser(t, app, "fork-cap@example.test")

	source, err := chat.CreateSession(app, chat.NewSession{
		UserID:       userID,
		Kind:         chat.KindSearch,
		Mode:         chat.ModeResearch,
		FirstMessage: "the first of many",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 1; i < chat.MaxSessionsPerUser; i++ {
		if _, err := chat.CreateSession(app, chat.NewSession{
			UserID:       userID,
			Kind:         chat.KindSearch,
			Mode:         chat.ModeSearch,
			FirstMessage: "filler",
		}); err != nil {
			t.Fatalf("CreateSession(%d): %v", i, err)
		}
	}

	if _, err := chat.ForkSession(app, userID, source, ""); !errors.Is(err, chat.ErrTooManySessions) {
		t.Fatalf("ForkSession at the cap = %v, want ErrTooManySessions", err)
	}
	if total, err := chat.CountSessions(app, userID); err != nil || total != chat.MaxSessionsPerUser {
		t.Fatalf("CountSessions() = %d, %v; want %d sessions and no error", total, err, chat.MaxSessionsPerUser)
	}
}
