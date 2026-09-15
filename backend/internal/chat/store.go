package chat

import (
	"fmt"
	"slices"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
)

// SessionQuery selects a slice of one account's sessions. An empty Kind or
// DocumentID means "any".
type SessionQuery struct {
	UserID     string
	Kind       Kind
	DocumentID string
	Offset     int
	Limit      int
}

type NewSession struct {
	UserID     string
	Kind       Kind
	DocumentID string
	// Mode is the search mode the conversation runs in, and is fixed for its
	// lifetime. Empty for document chats.
	Mode string
	// Binding is the provider and model the conversation runs on, fixed for its
	// lifetime for the same reason Mode is. Empty means the Settings binding.
	Binding aiprovider.Binding
	// FirstMessage is the question that started the conversation; the title is
	// derived from it.
	FirstMessage string
}

type Turn struct {
	UserContent      string
	AssistantContent string
	// RunID correlates the stored pair with the client request that produced
	// it. Empty preserves transcripts written by older clients.
	RunID     string
	Documents []ai.DocumentHit
	// Steps is the research trail shown while the run was live. Empty for
	// Search and for Ask AI. Not replayed to the model.
	Steps []StoredStep
	// Usage is how much context the answer took, for the line a reopened chat
	// shows under it. Zero for Search and for Ask AI.
	Usage ai.TurnUsage
	// Incomplete marks an assistant answer that was cut off mid-generation.
	Incomplete bool
	// Mode records which search mode produced the answer; empty leaves the
	// session's current value alone.
	Mode string
}

// FindOwnedSession resolves one of the account's own sessions. Scoping the
// query by user rather than checking ownership afterwards is what keeps a
// session from reading another account's transcript by guessing a record id.
func FindOwnedSession(app core.App, userID, sessionID string) (*core.Record, error) {
	if userID == "" || sessionID == "" {
		return nil, ErrNotFound
	}
	records := []*core.Record{}
	err := app.RecordQuery(SessionsCollection).
		AndWhere(dbx.HashExp{"id": sessionID, "user": userID}).
		Limit(1).
		All(&records)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrNotFound
	}
	return records[0], nil
}

// ListSessions returns one page of the account's sessions, newest activity
// first, along with the unpaginated total. Ordered by last_message_at rather
// than updated so renaming a chat leaves it where the user expects it; the id
// tiebreak keeps the order stable across pages.
func ListSessions(app core.App, q SessionQuery) ([]*core.Record, int, error) {
	if q.UserID == "" {
		return nil, 0, fmt.Errorf("list chat sessions: user id is required")
	}

	where := sessionFilter(q)

	total, err := app.CountRecords(SessionsCollection, where)
	if err != nil {
		return nil, 0, err
	}

	records := []*core.Record{}
	query := app.RecordQuery(SessionsCollection).
		AndWhere(where).
		OrderBy("last_message_at DESC", "id DESC")
	if q.Limit > 0 {
		query = query.Limit(int64(q.Limit))
	}
	if q.Offset > 0 {
		query = query.Offset(int64(q.Offset))
	}
	if err := query.All(&records); err != nil {
		return nil, 0, err
	}
	return records, int(total), nil
}

// CountSessions counts everything the account owns, across both kinds.
func CountSessions(app core.App, userID string) (int, error) {
	total, err := app.CountRecords(SessionsCollection, dbx.HashExp{"user": userID})
	return int(total), err
}

func sessionFilter(q SessionQuery) dbx.Expression {
	exp := dbx.HashExp{"user": q.UserID}
	if q.Kind != "" {
		exp["kind"] = string(q.Kind)
	}
	if q.DocumentID != "" {
		exp["document"] = q.DocumentID
	}
	return exp
}

func ListMessages(app core.App, sessionID string, limit int) ([]*core.Record, error) {
	records := []*core.Record{}
	query := app.RecordQuery(MessagesCollection).AndWhere(dbx.HashExp{"session": sessionID})

	if limit <= 0 {
		if err := query.OrderBy("seq ASC").All(&records); err != nil {
			return nil, err
		}
		return records, nil
	}

	// The newest `limit` turns, not the oldest, which is why the read is
	// ordered backwards and reversed. The tail is what the user is looking at
	// and what the next question follows from.
	if err := query.OrderBy("seq DESC").Limit(int64(limit)).All(&records); err != nil {
		return nil, err
	}
	slices.Reverse(records)
	return records, nil
}

// History returns the prior turns to replay to the model, whole. Nothing here
// decides what fits: the provider's context window is the only limit, and a
// request that exceeds it comes back as an error the user is shown.
func History(app core.App, sessionID string) ([]ai.ChatMessage, error) {
	records, err := ListMessages(app, sessionID, MaxReplayMessages)
	if err != nil {
		return nil, err
	}
	messages := make([]ai.ChatMessage, 0, len(records))
	for _, record := range records {
		messages = append(messages, ai.ChatMessage{
			Role:    record.GetString("role"),
			Content: record.GetString("content"),
		})
	}
	return messages, nil
}

// MaxPriorHits caps the evidence one conversation carries forward. Well past
// what a transcript that fits the replay budget can hold, so it is a guard
// against a pathological session rather than a working limit.
const MaxPriorHits = 100

// PriorHits returns the documents a session's earlier answers found, so a
// follow-up can read one by id instead of guessing a query that would
// rediscover it. Latest wins on a repeat. Passages are dropped: they were
// selected for the question that turn asked.
func PriorHits(app core.App, sessionID string) ([]ai.DocumentHit, error) {
	if sessionID == "" {
		return nil, nil
	}
	records, err := ListMessages(app, sessionID, MaxReplayMessages)
	if err != nil {
		return nil, err
	}
	return PriorHitsFrom(records), nil
}

// PriorHitsFrom is PriorHits over an already-loaded transcript, in the order
// the turns happened.
func PriorHitsFrom(records []*core.Record) []ai.DocumentHit {
	// Newest first, so the cap keeps the most recent evidence.
	records = slices.Clone(records)
	slices.Reverse(records)

	hits := make([]ai.DocumentHit, 0, MaxPriorHits)
	seen := map[string]struct{}{}
	for _, record := range records {
		if record.GetString("role") != RoleAssistant {
			continue
		}
		for _, hit := range DecodeHits(record) {
			if hit.ID == "" {
				continue
			}
			if _, ok := seen[hit.ID]; ok {
				continue
			}
			seen[hit.ID] = struct{}{}
			hit.Passages = nil
			hits = append(hits, hit)
			if len(hits) >= MaxPriorHits {
				return hits
			}
		}
	}
	return hits
}

// CreateSession opens an empty conversation, before the provider is called
// rather than after it answers: the record is what the turn, the sidebar row
// and the provider's cache key all name, and the session cap is then refused
// before a provider call is spent. An empty session is a real one, so a first
// turn that never completes has to be cleaned up: see DiscardEmptySession.
func CreateSession(app core.App, spec NewSession) (*core.Record, error) {
	var session *core.Record

	err := app.RunInTransaction(func(txApp core.App) error {
		total, err := CountSessions(txApp, spec.UserID)
		if err != nil {
			return err
		}
		if total >= MaxSessionsPerUser {
			return ErrTooManySessions
		}
		collection, err := txApp.FindCollectionByNameOrId(SessionsCollection)
		if err != nil {
			return err
		}
		session = core.NewRecord(collection)
		session.Set("user", spec.UserID)
		session.Set("kind", string(spec.Kind))
		if spec.DocumentID != "" {
			session.Set("document", spec.DocumentID)
		}
		if spec.Mode != "" {
			session.Set("mode", spec.Mode)
		}
		if binding := spec.Binding.Normalized(); !binding.Empty() {
			session.Set("provider", binding.ProviderID)
			session.Set("model", binding.Model)
		}
		session.Set("title", DeriveTitle(spec.FirstMessage))
		session.Set("message_count", 0)
		// Stamped now rather than left empty, so a session whose first answer
		// is still being generated sorts where the user expects in a sidebar
		// ordered by last_message_at. AppendTurn moves it again.
		session.Set("last_message_at", types.NowDateTime())
		return txApp.Save(session)
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// DiscardEmptySession removes a conversation that never got a turn. Guarded on
// the transcript rather than on message_count, which a caller holding a stale
// record could read as 0 after a turn had landed.
func DiscardEmptySession(app core.App, session *core.Record) error {
	if session == nil {
		return nil
	}
	total, err := app.CountRecords(MessagesCollection, dbx.HashExp{"session": session.Id})
	if err != nil {
		return err
	}
	if total > 0 {
		return nil
	}
	return app.Delete(session)
}

// AppendTurn writes both halves of one exchange into an existing session,
// re-reading it under the owner so a request cannot append to someone else's
// conversation. Called only after the model has answered: writing the user
// message up front would leave a dangling half-turn behind every failure, and
// feed the next request a duplicated question. Nothing here consults the
// request context, so a client that disconnects mid-response still finds the
// turn on reload.
func AppendTurn(app core.App, userID, sessionID string, turn Turn) (*core.Record, error) {
	var session *core.Record

	err := app.RunInTransaction(func(txApp core.App) error {
		var err error
		session, err = FindOwnedSession(txApp, userID, sessionID)
		if err != nil {
			return err
		}

		next, err := nextSeq(txApp, session.Id)
		if err != nil {
			return err
		}

		if err := saveMessage(txApp, session.Id, next, RoleUser, turn.UserContent, turn.RunID, nil, nil, ai.TurnUsage{}, false); err != nil {
			return err
		}
		if err := saveMessage(txApp, session.Id, next+1, RoleAssistant, turn.AssistantContent, turn.RunID, turn.Documents, turn.Steps, turn.Usage, turn.Incomplete); err != nil {
			return err
		}

		session.Set("message_count", session.GetInt("message_count")+2)
		session.Set("last_message_at", types.NowDateTime())
		if turn.Mode != "" {
			session.Set("mode", turn.Mode)
		}
		return txApp.Save(session)
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// nextSeq allocates the next turn number. Read-modify-write is safe because
// PocketBase serializes writes onto one connection and this runs inside the
// transaction; the unique (session, seq) index is the belt to that braces.
func nextSeq(app core.App, sessionID string) (int, error) {
	var highest struct {
		Value int `db:"value"`
	}
	err := app.DB().
		Select("COALESCE(MAX(seq), 0) AS value").
		From(MessagesCollection).
		Where(dbx.HashExp{"session": sessionID}).
		One(&highest)
	if err != nil {
		return 0, err
	}
	return highest.Value + 1, nil
}

func saveMessage(app core.App, sessionID string, seq int, role, content, runID string, hits []ai.DocumentHit, steps []StoredStep, usage ai.TurnUsage, incomplete bool) error {
	collection, err := app.FindCollectionByNameOrId(MessagesCollection)
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("session", sessionID)
	record.Set("seq", seq)
	record.Set("role", role)
	record.Set("run_id", runID)
	// Truncated rather than rejected: the answer is already paid for, and a
	// validation error here would discard it. See MaxMessageRunes.
	record.Set("content", FitColumn(content, MaxMessageRunes))
	if encoded := EncodeHits(hits); encoded != nil {
		record.Set("documents", encoded)
	}
	if role == RoleAssistant {
		if encoded := EncodeSteps(steps); encoded != nil {
			record.Set("steps", encoded)
		}
		if encoded := EncodeUsage(usage); encoded != nil {
			record.Set("usage", encoded)
		}
		if incomplete {
			record.Set("incomplete", true)
		}
	}
	return app.Save(record)
}

// RenameSession applies a user-supplied title. last_message_at is deliberately
// untouched, so renaming does not reorder the sidebar under the person doing it.
func RenameSession(app core.App, record *core.Record, title string) error {
	record.Set("title", NormalizeTitle(title))
	return app.Save(record)
}

// DeleteSession removes a session; its messages follow by cascade.
func DeleteSession(app core.App, record *core.Record) error {
	return app.Delete(record)
}
