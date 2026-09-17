package chat

import (
	"fmt"
	"slices"
	"unicode/utf8"

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

// Thread returns the conversation as it was sent to the provider: the system
// prompt it was opened with, every question, every tool call and result, and
// every answer. Replaying it is what lets a follow-up build on the work earlier
// turns did instead of repeating it, and what gives the provider a prefix it
// has already cached.
//
// Read whole, with no row cap. A cap would slide the window as the
// conversation grew, dropping the system prompt off the front and moving the
// prefix under the provider's cache on every turn; what the model can hold is
// the model's business, and it says so by refusing. TrimThread still tidies the
// ends, which a fork or an interrupted run can leave ragged.
func Thread(app core.App, sessionID string) ([]ai.ThreadMessage, error) {
	records, err := ListMessages(app, sessionID, 0)
	if err != nil {
		return nil, err
	}
	thread := make([]ai.ThreadMessage, 0, len(records))
	for _, record := range records {
		thread = append(thread, ai.ThreadMessage{
			Role:    record.GetString("role"),
			Content: record.GetString("content"),
			Calls:   DecodeToolCalls(record),
			CallID:  record.GetString("tool_call_id"),
		})
	}
	return ai.TrimThread(thread), nil
}

// AppendThreadMessage writes one row of a research conversation as it happens,
// rather than the whole turn once it has finished. That is what keeps the work
// of a run that dies -- the documents it read are on disk before the answer
// that would have cited them exists.
//
// The session is re-read under its owner on every append, exactly as AppendTurn
// does, so a request cannot write into someone else's conversation.
func AppendThreadMessage(app core.App, userID, sessionID string, msg ThreadEntry) (*core.Record, error) {
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
		if err := saveMessage(txApp, session.Id, next, storedMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			RunID:      msg.RunID,
			Calls:      msg.Calls,
			CallID:     msg.CallID,
			Documents:  msg.Documents,
			Steps:      msg.Steps,
			Usage:      msg.Usage,
			Incomplete: msg.Incomplete,
		}); err != nil {
			return err
		}

		// Counted as a person counts them: the machinery underneath a turn is
		// not what the sidebar means by a message.
		if Visible(msg.Role, msg.Content, len(msg.Calls) > 0) {
			session.Set("message_count", session.GetInt("message_count")+1)
		}
		session.Set("last_message_at", types.NowDateTime())
		if msg.Mode != "" {
			session.Set("mode", msg.Mode)
		}
		return txApp.Save(session)
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// ThreadEntry is one appended row. Same shape as the internal write, with the
// session's mode alongside, so a caller outside the package can build one.
type ThreadEntry struct {
	Role       string
	Content    string
	RunID      string
	Calls      []ai.ToolCall
	CallID     string
	Documents  []ai.DocumentHit
	Steps      []StoredStep
	Usage      ai.TurnUsage
	Incomplete bool
	// Mode stamps the session on the first row of a turn; empty leaves it.
	Mode string `json:"-"`
}

// visibleCount is how many of these rows a person would count as messages. The
// tool calls and results between them are the turn's working, not the turn.
func visibleCount(records []*core.Record) int {
	total := 0
	for _, record := range records {
		if VisibleRecord(record) {
			total++
		}
	}
	return total
}

// snapToAnswer drops a trailing half-turn, so what is left ends where a person
// would say the conversation ended.
func snapToAnswer(records []*core.Record) []*core.Record {
	for len(records) > 0 {
		last := records[len(records)-1]
		if VisibleRecord(last) && last.GetString("role") == RoleAssistant {
			break
		}
		records = records[:len(records)-1]
	}
	return records
}

// Unfinished reports a conversation whose last row is not an answer: a run that
// died, was cancelled, or is still going. Derived rather than stored, so it is
// still true after a restart, which the in-process run registry is not.
func Unfinished(records []*core.Record) bool {
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		role := record.GetString("role")
		if role == RoleSystem {
			continue
		}
		// Finished means answered. A transcript that ends on a question, on a
		// tool result, or on a call nothing answered is a turn that stopped
		// somewhere in the middle.
		return !(role == RoleAssistant && VisibleRecord(record))
	}
	return false
}

// MaxPriorHits caps the evidence one conversation carries forward, newest
// first. A guard against a pathological session rather than a working limit:
// the thread replays the tool results these came from anyway, so this is the
// shortcut to reading one by id, not the only record that it exists.
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

// ForkSession copies a conversation and its transcript into a new one, so a
// question can branch off an answer without disturbing the chat it came from.
// Kind, mode and binding travel with it because a session refuses a turn that
// contradicts them, and the hits are re-encoded rather than re-resolved: they
// are a snapshot of what the answer cited. run_id is deliberately dropped --
// it correlates a stored turn with the live request that produced it, and the
// copy produced none.
//
// upto names the last message the copy keeps, for branching off an answer with
// a conversation already past it; empty takes the whole transcript. A message
// id the source does not hold is ErrNotFound rather than the whole transcript:
// a client asking to branch somewhere that is no longer there means a trim or
// another window moved under it, and a full copy is not what it asked for.
func ForkSession(app core.App, userID string, source *core.Record, upto string) (*core.Record, error) {
	var session *core.Record

	err := app.RunInTransaction(func(txApp core.App) error {
		total, err := CountSessions(txApp, userID)
		if err != nil {
			return err
		}
		if total >= MaxSessionsPerUser {
			return ErrTooManySessions
		}
		messages, err := ListMessages(txApp, source.Id, 0)
		if err != nil {
			return err
		}
		if upto != "" {
			cut := -1
			for i, message := range messages {
				if message.Id == upto {
					cut = i
					break
				}
			}
			if cut < 0 {
				return ErrNotFound
			}
			messages = messages[:cut+1]
		}
		// A research transcript is the provider array, so a cut can land
		// between a tool call and its result. Snapped back to the last finished
		// answer: a fork is a conversation to continue, not a turn to resume.
		messages = snapToAnswer(messages)
		collection, err := txApp.FindCollectionByNameOrId(SessionsCollection)
		if err != nil {
			return err
		}

		session = core.NewRecord(collection)
		session.Set("user", userID)
		session.Set("kind", source.GetString("kind"))
		if document := source.GetString("document"); document != "" {
			session.Set("document", document)
		}
		if mode := source.GetString("mode"); mode != "" {
			session.Set("mode", mode)
		}
		session.Set("provider", source.GetString("provider"))
		session.Set("model", source.GetString("model"))
		session.Set("title", ForkTitle(source.GetString("title")))
		session.Set("message_count", visibleCount(messages))
		// Now rather than the source's, so the fork is where the sidebar puts
		// what just happened.
		session.Set("last_message_at", types.NowDateTime())
		if err := txApp.Save(session); err != nil {
			return err
		}

		// Renumbered from 1: seq is unique per session and only has to order
		// this transcript, and the source's may start past 1 after a trim.
		for i, message := range messages {
			// Copied with the turn: the fork's transcript is the source's, and
			// an answer that arrived near the model's limit still did.
			usage := ai.TurnUsage{}
			if stored := DecodeUsage(message); stored != nil {
				usage = *stored
			}
			if err := saveMessage(txApp, session.Id, i+1, storedMessage{
				Role:       message.GetString("role"),
				Content:    message.GetString("content"),
				Calls:      DecodeToolCalls(message),
				CallID:     message.GetString("tool_call_id"),
				Documents:  DecodeHits(message),
				Steps:      DecodeSteps(message),
				Usage:      usage,
				Incomplete: message.GetBool("incomplete"),
			}); err != nil {
				return err
			}
		}
		return nil
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

		if err := saveMessage(txApp, session.Id, next, storedMessage{
			Role:    RoleUser,
			Content: turn.UserContent,
			RunID:   turn.RunID,
		}); err != nil {
			return err
		}
		if err := saveMessage(txApp, session.Id, next+1, storedMessage{
			Role:       RoleAssistant,
			Content:    turn.AssistantContent,
			RunID:      turn.RunID,
			Documents:  turn.Documents,
			Steps:      turn.Steps,
			Usage:      turn.Usage,
			Incomplete: turn.Incomplete,
		}); err != nil {
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

// storedMessage is one row on its way in. A struct rather than a dozen
// positional arguments: the row grew a tool call, a call id and a trail, and
// half of them are empty on any given write.
type storedMessage struct {
	Role    string
	Content string
	RunID   string
	// Calls and CallID carry a research thread's machinery: what an assistant
	// turn asked for, and which ask a tool turn answers.
	Calls      []ai.ToolCall
	CallID     string
	Documents  []ai.DocumentHit
	Steps      []StoredStep
	Usage      ai.TurnUsage
	Incomplete bool
}

// toolResultTooLarge stands in for a result past the column. Truncating one
// would be worse than losing it: the model reads a tool result as fact, and
// half a JSON object is a confidently wrong fact.
const toolResultTooLarge = `{"error":"the tool result was too large to store and was dropped"}`

func saveMessage(app core.App, sessionID string, seq int, msg storedMessage) error {
	collection, err := app.FindCollectionByNameOrId(MessagesCollection)
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("session", sessionID)
	record.Set("seq", seq)
	record.Set("role", msg.Role)
	record.Set("run_id", msg.RunID)
	record.Set("content", fitContent(msg.Role, msg.Content))
	if msg.CallID != "" {
		record.Set("tool_call_id", FitColumn(msg.CallID, MaxToolCallIDRunes))
	}
	if encoded := EncodeToolCalls(msg.Calls); encoded != nil {
		record.Set("tool_calls", encoded)
	}
	if encoded := EncodeHits(msg.Documents); encoded != nil {
		record.Set("documents", encoded)
	}
	// Not assistant-only any more: a tool row carries the one step describing
	// it, which is what lets a turn whose run died still render its trail.
	if encoded := EncodeSteps(msg.Steps); encoded != nil {
		record.Set("steps", encoded)
	}
	if msg.Role == RoleAssistant {
		if encoded := EncodeUsage(msg.Usage); encoded != nil {
			record.Set("usage", encoded)
		}
		if msg.Incomplete {
			record.Set("incomplete", true)
		}
	}
	return app.Save(record)
}

// fitContent sizes a row's text by what it is. Prose is truncated rather than
// rejected -- the answer is already paid for, and a validation error here would
// discard it -- but a tool result is replaced whole rather than cut. A question
// gets the whole column: it is the one text a person typed, and cutting it
// would rewrite what they asked.
func fitContent(role, content string) string {
	switch role {
	case RoleTool:
		if utf8.RuneCountInString(content) > MaxThreadContentRunes {
			return toolResultTooLarge
		}
		return content
	case RoleSystem, RoleUser:
		return FitColumn(content, MaxThreadContentRunes)
	}
	return FitColumn(content, MaxMessageRunes)
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
