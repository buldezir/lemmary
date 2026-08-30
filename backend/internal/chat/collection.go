// Package chat stores the AI conversations behind Deep Search and the
// per-document Ask AI page.
//
// Both surfaces used to be stateless: the browser held the transcript in React
// state and replayed the whole message array on every request, so a reload lost
// the conversation. Here the server owns it -- a request carries a session id
// and one new message, and the history comes out of the database.
package chat

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	// SessionsCollection holds one conversation per record.
	SessionsCollection = "chat_sessions"
	// MessagesCollection holds one turn per record, ordered by seq.
	MessagesCollection = "chat_messages"

	// UsersCollectionName owns a session; DocumentsCollectionName is the
	// document a KindDocument session is about.
	UsersCollectionName     = "users"
	DocumentsCollectionName = "documents"
)

// EnsureCollections creates both collections if they are missing, so the
// migration and a fresh boot share one definition -- the same delegation
// internal/passkey and internal/aiprovider use.
//
// Neither collection gets API rules, which leaves them nil: PocketBase then
// serves them to superusers only, and /api/app/chats is the sole access path.
// That is the load-bearing choice here. A transcript is not user-editable data:
// with a create rule, a session could POST a chat_messages record with
// role="assistant" and arbitrary content, and the server would replay it to the
// model on the next turn as a genuine prior answer -- a self-service prompt
// injection channel into history the server treats as trusted. seq and
// message_count are server invariants for the same reason.
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
		// Set only for KindDocument. CascadeDelete because the alternative is
		// worse than it looks: for an optional relation PocketBase unsets the
		// id instead of blocking the delete, which would leave a document
		// session with no document -- listed in the sidebar, impossible to
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
		// the Deep mode toggle. Empty for document chats.
		&core.SelectField{
			Name:      "mode",
			MaxSelect: 1,
			Values:    []string{ModeShallow, ModeDeep},
		},
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
	// Not optional. Relating to documents means every document delete now looks
	// for referring sessions, and without this that is a full scan of a table
	// holding whole transcripts -- the same reasoning 1730000012 gives for
	// idx_processing_jobs_document.
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
		// The search hits the assistant turn was grounded in, so a replayed
		// transcript still renders its result cards. Not Required -- a JSON
		// field's Required rejects an empty array, and user turns have none.
		&core.JSONField{Name: "documents", MaxSize: MaxHitsJSONBytes},
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
