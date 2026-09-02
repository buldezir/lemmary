package chat

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/strutil"
)

// Kind separates the two chat surfaces sharing this collection. They have the
// same lifecycle and the same sidebar, so one collection with a discriminator
// beats two near-identical ones.
type Kind string

const (
	KindSearch   Kind = "search"
	KindDocument Kind = "document"
)

// Roles a stored turn can carry. Tool calls and system prompts stay inside the
// agent loop: only what the user typed and what they were shown is persisted.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// The two things a search turn can be: find documents and list them, or read
// them and answer with citations. Plain strings so the collection definition
// does not drag in the ai package.
const (
	ModeSearch   = "search"
	ModeResearch = "research"
)

// UntitledSession is what a session is called when nothing usable can be
// derived from its first message.
const UntitledSession = "New chat"

const (
	// MaxTitleRunes is how much of the first user message becomes the title.
	MaxTitleRunes = 80
	// MaxTitleColumnRunes gives the title column headroom over MaxTitleRunes,
	// so a rename is not forced into the derived length.
	MaxTitleColumnRunes = 120

	// MaxUserContentRunes is the largest message the API accepts. Rejected
	// rather than truncated: silently sending the model half a question is
	// worse than saying no.
	MaxUserContentRunes = 8000
	// MaxMessageRunes bounds the content column. An assistant reply longer
	// than this is truncated on the way in, never rejected -- 1730000016 is
	// what a column Max the producer did not know about costs, and here it
	// would mean throwing away an answer the provider was already paid for.
	MaxMessageRunes = 60000

	// MaxHistoryMessages and MaxHistoryRunes bound the transcript replayed to
	// the model. The rune budget matters most for Deep Search: its agent loop
	// resends the whole array on each of up to five rounds.
	MaxHistoryMessages = 40
	MaxHistoryRunes    = 24000

	// MaxSessionsPerUser stops an account from turning the sidebar into an
	// unbounded table. Breaching it is an error, never a silent prune.
	MaxSessionsPerUser = 500

	// MaxHitsPerTurn and MaxHitsJSONBytes bound the search hits stored beside
	// an assistant turn.
	MaxHitsPerTurn   = 50
	MaxHitsJSONBytes = 64000

	// MaxReplayMessages caps one transcript read. Sessions do not get near it
	// in practice; the cap exists so a single request cannot load an unbounded
	// number of rows.
	MaxReplayMessages = 500
)

var (
	// ErrNotFound covers both "no such session" and "belongs to someone else",
	// deliberately as one error so a caller cannot use the distinction to probe
	// for other accounts' session ids. Same reasoning as passkey.ErrNotFound.
	ErrNotFound = errors.New("chat session not found")

	// ErrTooManySessions is MaxSessionsPerUser refusing a new session.
	ErrTooManySessions = errors.New("too many chat sessions")
)

// ParseKind resolves a client-supplied kind. The bool distinguishes "not given"
// from "not valid" at the call site.
func ParseKind(raw string) (Kind, bool) {
	switch Kind(strings.ToLower(strings.TrimSpace(raw))) {
	case KindSearch:
		return KindSearch, true
	case KindDocument:
		return KindDocument, true
	default:
		return "", false
	}
}

// DeriveTitle names a session after the message that started it.
//
// Whitespace is collapsed first: a pasted multi-line question would otherwise
// put newlines into a sidebar row, and the visible part would be only its first
// line however long the rest is.
func DeriveTitle(firstUserMessage string) string {
	collapsed := strings.Join(strings.Fields(firstUserMessage), " ")
	if collapsed == "" {
		return UntitledSession
	}
	return strutil.TruncateRunes(collapsed, MaxTitleRunes)
}

// NormalizeTitle cleans a user-supplied rename, falling back to the placeholder
// rather than rejecting a blank one -- the same forgiving shape as
// passkey.NormalizeName.
func NormalizeTitle(title string) string {
	collapsed := strings.Join(strings.Fields(title), " ")
	if collapsed == "" {
		return UntitledSession
	}
	return FitColumn(collapsed, MaxTitleColumnRunes)
}

// FitColumn shortens s to something a column of max runes will accept.
//
// The -1 is not an off-by-one: TruncateRunes appends an ellipsis, so cutting to
// exactly max hands back max+1 runes and the save fails validation -- which is
// the failure mode 1730000016 already paid for once.
func FitColumn(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	// No room for content and an ellipsis both; TruncateRunes would also read a
	// budget of 0 as "no limit" and hand the whole string back.
	if max == 1 {
		return string([]rune(s)[:1])
	}
	return strutil.TruncateRunes(s, max-1)
}

// ClampHistory trims a transcript to what is worth replaying to the model:
// the most recent MaxHistoryMessages turns, and within that the most recent
// MaxHistoryRunes of text.
//
// Two shape rules the caps must not break. The last message is the question
// being asked, so it survives even when it alone exceeds the rune budget --
// dropping it would send the model a conversation with no request in it. And
// the window never opens on an assistant turn, which reads as though the user's
// question had been edited out.
func ClampHistory(messages []ai.ChatMessage) []ai.ChatMessage {
	if len(messages) == 0 {
		return []ai.ChatMessage{}
	}

	start := 0
	if len(messages) > MaxHistoryMessages {
		start = len(messages) - MaxHistoryMessages
	}

	// Walk backwards adding whole messages while the budget lasts, always
	// keeping the last one.
	budget := MaxHistoryRunes
	first := len(messages) - 1
	for i := len(messages) - 1; i >= start; i-- {
		cost := utf8.RuneCountInString(messages[i].Content)
		if i < len(messages)-1 && cost > budget {
			break
		}
		budget -= cost
		first = i
	}
	if first < start {
		first = start
	}

	// Never start mid-answer.
	if first < len(messages)-1 && messages[first].Role == RoleAssistant {
		first++
	}

	out := make([]ai.ChatMessage, 0, len(messages)-first)
	out = append(out, messages[first:]...)
	return out
}

// EncodeHits renders the search hits stored beside an assistant turn.
//
// They are a snapshot, not relations: the reply text describes what was found
// at that moment, and re-resolving the documents later would let a retitled or
// re-summarized document make the transcript disagree with itself.
//
// Over budget, the long free-text fields go before any hit does -- losing a
// snippet costs a preview line, losing a hit costs a result card the answer
// refers to by name. Passages go first of all: they are by far the largest
// field, and the snippet already carries the best of them shortened, which is
// all the card ever shows.
func EncodeHits(hits []ai.DocumentHit) types.JSONRaw {
	if len(hits) == 0 {
		return nil
	}
	if len(hits) > MaxHitsPerTurn {
		hits = hits[:MaxHitsPerTurn]
	}

	encoded, err := json.Marshal(hits)
	if err != nil {
		return nil
	}
	if len(encoded) <= MaxHitsJSONBytes {
		return types.JSONRaw(encoded)
	}

	trimmed := make([]ai.DocumentHit, len(hits))
	copy(trimmed, hits)
	for i := range trimmed {
		trimmed[i].Passages = nil
	}
	if encoded, err = json.Marshal(trimmed); err != nil {
		return nil
	}
	if len(encoded) <= MaxHitsJSONBytes {
		return types.JSONRaw(encoded)
	}

	for i := range trimmed {
		trimmed[i].OCRSnippet = ""
		trimmed[i].Summary = ""
	}
	for {
		encoded, err = json.Marshal(trimmed)
		if err != nil {
			return nil
		}
		if len(encoded) <= MaxHitsJSONBytes || len(trimmed) == 0 {
			break
		}
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 {
		return nil
	}
	return types.JSONRaw(encoded)
}

// DecodeHits reads the hits back off a message record.
//
// The raw-string dance is not defensive padding: PocketBase hands a JSON field
// back as a typed value after a save and as a raw string after a fresh read,
// the same polymorphism models.PeopleOrOrganizations documents.
func DecodeHits(record *core.Record) []ai.DocumentHit {
	raw := strings.TrimSpace(record.GetString("documents"))
	if raw == "" || raw == "null" {
		return nil
	}
	var hits []ai.DocumentHit
	if err := json.Unmarshal([]byte(raw), &hits); err != nil {
		return nil
	}
	return hits
}

// SessionInfo is the client-facing view of a session.
type SessionInfo struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Title is what the sidebar shows.
	Title string `json:"title"`
	// Mode is the search mode the last turn ran in ("" for document chats).
	Mode string `json:"mode,omitempty"`
	// Document is set for KindDocument sessions; DocumentTitle is filled by the
	// handler, which is the layer that may read the documents collection.
	Document      string `json:"document,omitempty"`
	DocumentTitle string `json:"document_title,omitempty"`
	MessageCount  int    `json:"message_count"`
	LastMessageAt string `json:"last_message_at"`
	Created       string `json:"created"`
	Updated       string `json:"updated"`
}

// MessageInfo is the client-facing view of one turn.
type MessageInfo struct {
	ID        string           `json:"id"`
	Seq       int              `json:"seq"`
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Documents []ai.DocumentHit `json:"documents,omitempty"`
	Created   string           `json:"created"`
}

// ToSessionInfo projects a session record for the API.
func ToSessionInfo(record *core.Record) SessionInfo {
	lastMessageAt := ""
	if value := record.GetDateTime("last_message_at"); !value.IsZero() {
		lastMessageAt = value.String()
	}
	return SessionInfo{
		ID:            record.Id,
		Kind:          record.GetString("kind"),
		Title:         record.GetString("title"),
		Mode:          record.GetString("mode"),
		Document:      record.GetString("document"),
		MessageCount:  record.GetInt("message_count"),
		LastMessageAt: lastMessageAt,
		Created:       record.GetDateTime("created").String(),
		Updated:       record.GetDateTime("updated").String(),
	}
}

// ToMessageInfo projects a message record for the API.
func ToMessageInfo(record *core.Record) MessageInfo {
	return MessageInfo{
		ID:        record.Id,
		Seq:       record.GetInt("seq"),
		Role:      record.GetString("role"),
		Content:   record.GetString("content"),
		Documents: DecodeHits(record),
		Created:   record.GetDateTime("created").String(),
	}
}
