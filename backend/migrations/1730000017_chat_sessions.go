package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/chat"
)

// chat_sessions and chat_messages make Deep Search and the per-document Ask AI
// page resumable.
//
// Both were stateless: the browser held the conversation in component state and
// replayed the whole message array on every request, so reloading the tab or
// following a search hit threw the conversation away. Moving it server-side is
// what buys a chat list, a shareable URL per conversation, and a history the
// user can come back to -- and it also means the transcript the model is shown
// is one this side wrote.
//
// Two collections rather than a messages JSON column on the session. A column
// would have to be read, appended to and rewritten for every turn, which is a
// lost update the moment two tabs answer into one conversation; each assistant
// turn carries its own search-hit payload; and a blob cannot be ordered by an
// index, which the next paragraph turns out to need.
//
// One collection with a kind discriminator rather than one per surface. The two
// have the same lifecycle, the same owner, the same sidebar and the same CRUD;
// splitting them would duplicate all of it to express a difference that is one
// column wide.
//
// seq exists because ordering cannot rest on `created`. PocketBase timestamps
// are millisecond-precision and record ids are random, so the two messages of
// one exchange -- written inside a single transaction, microseconds apart --
// routinely share a timestamp with no tiebreak, and the answer sorts before the
// question about half the time. seq also carries a unique (session, seq) index,
// which makes concurrent writers a failed transaction instead of a transcript
// interleaved into user, user, assistant, assistant. It is 1-based: a
// NumberField's Required rejects 0.
//
// last_message_at is separate from `updated` so that renaming a chat does not
// shuffle it to the top of a list ordered by activity.
//
// chat_sessions.document cascades. Note this is not the situation 1730000009
// dealt with -- that one was about a *required* relation blocking deletion. An
// optional relation does not block: PocketBase unsets the id instead, which
// would leave a document session pointing at nothing, listed in the sidebar,
// impossible to continue, and quoting text that no longer exists. Going away
// with the document is the honest behavior.
//
// The schema itself lives in internal/chat.EnsureCollections so this migration
// and a fresh boot cannot drift apart -- the delegation 1730000007 and
// 1730000014 already use. As with passkeys, the collections get no API rules,
// which keeps them off /api/collections entirely: a session able to create its
// own chat_messages record could write role="assistant" content that the server
// then replays to the model as a prior answer, which is prompt injection with
// the server holding the door.
func init() {
	m.Register(func(app core.App) error {
		return chat.EnsureCollections(app)
	}, func(app core.App) error {
		// Messages first: their session relation is required, so dropping the
		// sessions collection while they exist would strand them.
		for _, name := range []string{chat.MessagesCollection, chat.SessionsCollection} {
			collection, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				continue
			}
			if err := app.Delete(collection); err != nil {
				return err
			}
		}
		return nil
	})
}
