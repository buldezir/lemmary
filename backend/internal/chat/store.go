package chat

import (
	"fmt"
	"slices"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/ai"
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

// NewSession describes the session AppendTurn creates when none was given.
type NewSession struct {
	UserID     string
	Kind       Kind
	DocumentID string
}

// Turn is one exchange: what the user asked and what the model answered.
type Turn struct {
	UserContent      string
	AssistantContent string
	Documents        []ai.DocumentHit
	// Mode records which search mode produced the answer; empty leaves the
	// session's current value alone.
	Mode string
}

// FindOwnedSession resolves one of the account's own sessions.
//
// Scoping the query by user rather than checking ownership after the fact is
// what keeps an authenticated session from reading another account's
// transcript by guessing a record id.
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
// first, along with the unpaginated total.
//
// Ordered by last_message_at rather than updated so renaming a chat leaves it
// where the user expects to find it. The id tiebreak keeps the order stable
// across pages when several sessions share a timestamp.
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

// ListMessages returns a session's turns in the order they happened.
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
	// ordered backwards and reversed rather than simply limited. A cap has to
	// drop the head of a transcript: the tail is the part the user is looking
	// at, and the part the next question follows from. Limiting an ascending
	// read returns the far end of a long session -- a stale window replayed to
	// the model, under a question that answers something else.
	if err := query.OrderBy("seq DESC").Limit(int64(limit)).All(&records); err != nil {
		return nil, err
	}
	slices.Reverse(records)
	return records, nil
}

// History returns the prior turns to replay to the model, already clamped to
// the budget in ClampHistory.
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
	return ClampHistory(messages), nil
}

// MaxPriorHits caps the evidence one conversation carries forward. Well past
// what a transcript that fits the replay budget can hold, so it is a guard
// against a pathological session rather than a working limit.
const MaxPriorHits = 100

// PriorHits returns the documents a session's earlier answers found, so a
// follow-up question can read one by id instead of guessing a query that would
// rediscover it.
//
// Latest wins on a repeat: a document found again in a later turn is carried
// with that turn's metadata, at that turn's position. Passages are dropped —
// they were selected for the question that turn asked, and quoting them under a
// different one is misleading.
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
	// Newest first, so the first version of a document seen while walking
	// backwards is the newest one and the cap keeps the most recent evidence.
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

// AppendTurn writes both halves of one exchange, creating the session first
// when sessionID is empty. It returns the session record either way.
//
// Called only after the model has answered. Writing the user message up front
// would look more natural but leaves a dangling half-turn behind every failure
// -- and Deep Search makes up to five sequential provider calls per request, so
// abandoned tabs and provider timeouts are the ordinary case, not the edge. A
// transcript ending in an unanswered user message also feeds the next request a
// duplicated question. Nothing here consults the request context, so a client
// that disconnects while the response is being written still finds the turn on
// reload.
func AppendTurn(app core.App, sessionID string, spec NewSession, turn Turn) (*core.Record, error) {
	var session *core.Record

	err := app.RunInTransaction(func(txApp core.App) error {
		var err error

		if sessionID == "" {
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
			session.Set("title", DeriveTitle(turn.UserContent))
			session.Set("message_count", 0)
			// Saved before the messages because they need its id.
			if err := txApp.Save(session); err != nil {
				return err
			}
		} else {
			session, err = FindOwnedSession(txApp, spec.UserID, sessionID)
			if err != nil {
				return err
			}
		}

		next, err := nextSeq(txApp, session.Id)
		if err != nil {
			return err
		}

		if err := saveMessage(txApp, session.Id, next, RoleUser, turn.UserContent, nil); err != nil {
			return err
		}
		if err := saveMessage(txApp, session.Id, next+1, RoleAssistant, turn.AssistantContent, turn.Documents); err != nil {
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

// nextSeq allocates the next turn number for a session.
//
// Read-modify-write is safe here because PocketBase serializes writes onto one
// connection and this runs inside the transaction; the unique (session, seq)
// index is the belt to that braces, turning a concurrent second writer into a
// failed transaction rather than a scrambled transcript.
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

func saveMessage(app core.App, sessionID string, seq int, role, content string, hits []ai.DocumentHit) error {
	collection, err := app.FindCollectionByNameOrId(MessagesCollection)
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("session", sessionID)
	record.Set("seq", seq)
	record.Set("role", role)
	// Truncated rather than rejected: the answer is already paid for, and a
	// validation error here would discard it. See MaxMessageRunes.
	record.Set("content", FitColumn(content, MaxMessageRunes))
	if encoded := EncodeHits(hits); encoded != nil {
		record.Set("documents", encoded)
	}
	return app.Save(record)
}

// RenameSession applies a user-supplied title.
//
// last_message_at is deliberately untouched, so renaming does not reorder the
// sidebar out from under the person doing the renaming.
func RenameSession(app core.App, record *core.Record, title string) error {
	record.Set("title", NormalizeTitle(title))
	return app.Save(record)
}

// DeleteSession removes a session; its messages follow by cascade.
func DeleteSession(app core.App, record *core.Record) error {
	return app.Delete(record)
}
