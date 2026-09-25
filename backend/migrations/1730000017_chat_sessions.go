package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/chat"
)

// chat_sessions and chat_messages make Deep Search and the per-document Ask AI
// page resumable: both were stateless, so reloading the tab threw the
// conversation away, and the transcript the model is shown is now one this side
// wrote.
//
// Two collections rather than a messages JSON column: a column would have to be
// read, appended to and rewritten for every turn, which is a lost update the
// moment two tabs answer into one conversation, and a blob cannot be ordered by
// an index. One collection with a kind discriminator rather than one per
// surface, because the difference is one column wide.
//
// seq exists because ordering cannot rest on `created`: PocketBase timestamps
// are millisecond-precision and record ids are random, so the two messages of
// one exchange routinely share a timestamp and the answer sorts before the
// question about half the time. Its unique (session, seq) index makes concurrent
// writers a failed transaction instead of an interleaved transcript. It is
// 1-based: a NumberField's Required rejects 0.
//
// last_message_at is separate from `updated` so that renaming a chat does not
// shuffle it to the top of a list ordered by activity.
//
// chat_sessions.document cascades. An optional relation does not block deletion:
// PocketBase unsets the id instead, which would leave a session in the sidebar
// quoting text that no longer exists.
//
// The schema itself lives in internal/chat.EnsureCollections so this migration
// and a fresh boot cannot drift apart. As with passkeys, the collections get no
// API rules, which keeps them off /api/collections entirely: a session able to
// create its own chat_messages record could write role="assistant" content that
// the server then replays to the model as a prior answer.
func init() {
	m.Register(chat.EnsureCollections, func(app core.App) error {
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
