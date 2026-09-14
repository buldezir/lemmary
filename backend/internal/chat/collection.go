// Package chat stores the AI conversations behind Deep Search and the
// per-document Ask AI page.
//
// The server owns the transcript: a request carries a session id and one new
// message, and the history comes out of the database.
package chat

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	SessionsCollection = "chat_sessions"
	// MessagesCollection holds one turn per record, ordered by seq.
	MessagesCollection = "chat_messages"

	UsersCollectionName     = "users"
	DocumentsCollectionName = "documents"
)

// EnsureCollections creates both collections if they are missing, so the
// migration and a fresh boot share one definition.
//
// Neither collection gets API rules, which leaves them nil: PocketBase then
// serves them to superusers only, and /api/app/chats is the sole access path.
// That is load-bearing. With a create rule a session could POST a chat_messages
// record with role="assistant" and arbitrary content, and the server would
// replay it to the model as a genuine prior answer. seq and message_count are
// server invariants for the same reason.
func EnsureCollections(app core.App) error {
	sessions, err := ensureSessions(app)
	if err != nil {
		return err
	}
	return ensureMessages(app, sessions)
}

func ensureSessions(app core.App) (*core.Collection, error) {
	if collection, err := app.FindCollectionByNameOrId(SessionsCollection); err == nil {
		return collection, nil
	}

	users, err := app.FindCollectionByNameOrId(UsersCollectionName)
	if err != nil {
		return nil, fmt.Errorf("find %s collection: %w", UsersCollectionName, err)
	}
	documents, err := app.FindCollectionByNameOrId(DocumentsCollectionName)
	if err != nil {
		return nil, fmt.Errorf("find %s collection: %w", DocumentsCollectionName, err)
	}

	collection := core.NewBaseCollection(SessionsCollection)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "user",
			Required:      true,
			MaxSelect:     1,
			CollectionId:  users.Id,
			CascadeDelete: true,
		},
		&core.SelectField{
			Name:      "kind",
			Required:  true,
			MaxSelect: 1,
			Values:    []string{string(KindSearch), string(KindDocument)},
		},
		// Set only for KindDocument. CascadeDelete because for an optional
		// relation PocketBase unsets the id instead, leaving a session with no
		// document: listed in the sidebar and impossible to continue.
		// continue (the handler needs a document to read OCR text from), and
		// citing text that no longer exists.
		&core.RelationField{
			Name:          "document",
			MaxSelect:     1,
			CollectionId:  documents.Id,
			CascadeDelete: true,
		},
		&core.TextField{Name: "title", Max: MaxTitleColumnRunes},
		// The mode the last turn ran in, so reopening a search session restores
		// the Search/Research toggle. Empty for document chats.
		&core.SelectField{
			Name:      "mode",
			MaxSelect: 1,
			Values:    []string{ModeSearch, ModeResearch},
		},
		// The provider and model this conversation runs on, when it was opened
		// on something other than the configured binding. Empty means the
		// Settings binding. A text field rather than a relation: a transcript is
		// worth keeping after its provider row is gone, and a stale id falls back
		// the same way an empty one does.
		&core.TextField{Name: "provider", Max: 15},
		&core.TextField{Name: "model", Max: 200},
		// Not Required: a NumberField's Required means non-zero, and a session
		// legitimately holds 0 between its creation and its first turn inside
		// AppendTurn's transaction.
		&core.NumberField{Name: "message_count", OnlyInt: true, Min: types.Pointer(0.0)},
		// Sidebar ordering. Deliberately not `updated`: renaming a chat must
		// not shuffle it to the top of the list.
		&core.DateField{Name: "last_message_at"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	collection.AddIndex("idx_chat_sessions_user_last", false, "user, last_message_at", "")
	collection.AddIndex("idx_chat_sessions_user_kind_last", false, "user, kind, last_message_at", "")
	// Not optional: relating to documents means every document delete looks for
	// referring sessions, which without this is a full scan of a table holding
	// whole transcripts.
	collection.AddIndex("idx_chat_sessions_document", false, "document", "")

	if err := app.Save(collection); err != nil {
		return nil, fmt.Errorf("create %s collection: %w", SessionsCollection, err)
	}
	return collection, nil
}

func ensureMessages(app core.App, sessions *core.Collection) error {
	if _, err := app.FindCollectionByNameOrId(MessagesCollection); err == nil {
		return nil
	}

	collection := core.NewBaseCollection(MessagesCollection)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "session",
			Required:      true,
			MaxSelect:     1,
			CollectionId:  sessions.Id,
			CascadeDelete: true,
		},
		// 1-based, because a NumberField's Required rejects 0. See the
		// migration comment for why ordering cannot rest on `created`.
		&core.NumberField{Name: "seq", Required: true, OnlyInt: true, Min: types.Pointer(1.0)},
		&core.SelectField{
			Name:      "role",
			Required:  true,
			MaxSelect: 1,
			Values:    []string{RoleUser, RoleAssistant},
		},
		// Max is explicit on purpose: a TextField left at zero defaults to 5000
		// runes, which would reject most assistant replies.
		&core.TextField{Name: "content", Required: true, Max: MaxMessageRunes},
		// The client-generated id of the request that produced this pair, so a
		// dropped connection can recover its exact answer even when another tab
		// asks the same question concurrently.
		&core.TextField{Name: "run_id", Max: MaxRunIDRunes},
		// The search hits the assistant turn was grounded in, so a replayed
		// transcript still renders its result cards. Not Required -- a JSON
		// field's Required rejects an empty array, and user turns have none.
		&core.JSONField{Name: "documents", MaxSize: MaxHitsJSONBytes},
		// Research progress as the stream emitted it, so reopening a chat still
		// shows how the answer was produced. Empty on user turns and on Search.
		&core.JSONField{Name: "steps", MaxSize: MaxStepsJSONBytes},
		&core.BoolField{Name: "incomplete"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	// Unique, which makes it both the replay ordering index and a concurrency
	// guard: two tabs posting into one session cannot interleave into
	// user, user, assistant, assistant -- the second transaction fails here.
	collection.AddIndex("idx_chat_messages_session_seq", true, "session, seq", "")

	if err := app.Save(collection); err != nil {
		return fmt.Errorf("create %s collection: %w", MessagesCollection, err)
	}
	return nil
}
