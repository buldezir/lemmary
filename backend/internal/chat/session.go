package chat

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/strutil"
)

// Kind separates the two chat surfaces sharing this collection.
type Kind string

const (
	KindSearch   Kind = "search"
	KindDocument Kind = "document"
)

// Roles a stored row can carry. A research conversation stores the provider
// array itself, so the work a turn did survives the turn: the system prompt it
// was opened with, the tool calls it made, and what the tools answered. Search
// and Ask AI write only user and assistant rows, as they always did.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"
)

var ThreadRoles = []string{RoleUser, RoleAssistant, RoleTool, RoleSystem}

// Visible reports whether a row is part of the conversation a person reads.
// The others are the machinery underneath it, replayed to the model and folded
// into the research trail rather than rendered as a message. An assistant row
// that carries tool calls is machinery whatever it said on the way: a model
// that narrates its next search before making it is thinking out loud, and that
// belongs in the trail rather than in a bubble of its own.
//
// Tool calls come two ways. An OpenAI-shaped model fills tool_calls, which is
// hasCalls; the DSML dialect writes its invoke markup into the content and
// leaves the column empty, so that content is the same round by another route
// and is read here the same way.
func Visible(role, content string, hasCalls bool) bool {
	switch role {
	case RoleUser:
		return true
	case RoleAssistant:
		return !hasCalls && !ai.ContentHasDSMLToolCalls(content) && strings.TrimSpace(content) != ""
	default:
		return false
	}
}

// VisibleRecord is Visible over a stored row.
func VisibleRecord(record *core.Record) bool {
	return Visible(record.GetString("role"), record.GetString("content"), hasToolCalls(record))
}

func hasToolCalls(record *core.Record) bool {
	raw := strings.TrimSpace(record.GetString("tool_calls"))
	return raw != "" && raw != "null"
}

// The two things a search turn can be: find documents and list them, or read
// them and answer with citations.
const (
	ModeSearch   = "search"
	ModeResearch = "research"
)

const UntitledSession = "New chat"

const (
	// MaxTitleRunes is how much of the first user message becomes the title.
	MaxTitleRunes = 80
	// MaxTitleColumnRunes gives the title column headroom over MaxTitleRunes,
	// so a rename is not forced into the derived length.
	MaxTitleColumnRunes = 120

	// MaxMessageRunes bounds the content column. An assistant reply longer
	// than this is truncated on the way in, never rejected: the alternative is
	// throwing away an answer the provider was already paid for.
	MaxMessageRunes = 60000
	// MaxRunIDRunes bounds the client-generated correlation id stored beside a
	// turn. Not a credential; it only lets a client recover the exact answer of
	// a request whose connection was interrupted.
	MaxRunIDRunes = 200

	// MaxThreadContentRunes bounds the content column, which now holds tool
	// results as well as prose. Far above MaxMessageRunes because a read of
	// several documents dwarfs any answer, and a truncated tool result is a
	// corrupted one: the model would read half a JSON object as fact.
	MaxThreadContentRunes = 400000
	// MaxToolCallsJSONBytes bounds the calls one assistant turn may make.
	MaxToolCallsJSONBytes = 64000
	MaxToolCallIDRunes    = 200

	// MaxSessionsPerUser stops an account from turning the sidebar into an
	// unbounded table. Breaching it is an error, never a silent prune.
	MaxSessionsPerUser = 500

	MaxHitsPerTurn   = 50
	MaxHitsJSONBytes = 64000

	// MaxStepsPerTurn and MaxStepsJSONBytes bound the research trail stored
	// beside an assistant turn, so a malformed producer cannot bloat a row.
	MaxStepsPerTurn   = 80
	MaxStepsJSONBytes = 16000

	// MaxUsageJSONBytes bounds the context-usage record: three numbers, with
	// room for the field to grow.
	MaxUsageJSONBytes = 512

	// MaxReplayMessages caps one transcript read, so a single request cannot
	// load an unbounded number of rows. A read guard, not a context budget:
	// what fits the model is the model's business, and it says so by refusing.
	MaxReplayMessages = 500
)

var (
	// ErrNotFound covers both "no such session" and "belongs to someone else",
	// deliberately as one error so a caller cannot use the distinction to probe
	// for other accounts' session ids. Same reasoning as passkey.ErrNotFound.
	ErrNotFound = errors.New("chat session not found")

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

// DeriveTitle names a session after the message that started it. Whitespace is
// collapsed first: a pasted multi-line question would otherwise put newlines
// into a sidebar row.
func DeriveTitle(firstUserMessage string) string {
	collapsed := strings.Join(strings.Fields(firstUserMessage), " ")
	if collapsed == "" {
		return UntitledSession
	}
	return strutil.TruncateRunes(collapsed, MaxTitleRunes)
}

// NormalizeTitle cleans a user-supplied rename, falling back to the
// placeholder rather than rejecting a blank one.
func NormalizeTitle(title string) string {
	collapsed := strings.Join(strings.Fields(title), " ")
	if collapsed == "" {
		return UntitledSession
	}
	return FitColumn(collapsed, MaxTitleColumnRunes)
}

// ForkMark labels a conversation copied from another. The marks stack, so a
// fork of a fork carries two and the sidebar says how far from the original a
// conversation has been taken.
const ForkMark = "⑂"

// ForkTitle marks a copy of source's title. The marks gather at the front
// ("⑂⑂ title") rather than nesting a prefix per fork, and the result is cut
// to what the title column accepts.
func ForkTitle(title string) string {
	rest := strings.TrimSpace(title)
	marks := 1
	for strings.HasPrefix(rest, ForkMark) {
		marks++
		rest = strings.TrimPrefix(rest, ForkMark)
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		rest = UntitledSession
	}
	return FitColumn(strings.Repeat(ForkMark, marks)+" "+rest, MaxTitleColumnRunes)
}

// FitColumn shortens s to something a column of max runes will accept.
// The -1 is not an off-by-one: TruncateRunes appends an ellipsis, so cutting to
// exactly max hands back max+1 runes and the save fails validation.
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

// EncodeHits renders the search hits stored beside an assistant turn. They are
// a snapshot, not relations: re-resolving the documents later would let a
// retitled one make the transcript disagree with itself. Over budget, passages
// go first and the other free-text fields next, because losing a hit costs a
// result card the answer refers to by name.
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

// StoredStep is one research progress line as the stream emitted it. Folded
// into labels by the client; not replayed to the model.
type StoredStep struct {
	Kind      string   `json:"kind"`
	Status    string   `json:"status,omitempty"`
	Query     string   `json:"query,omitempty"`
	Titles    []string `json:"titles,omitempty"`
	Count     int      `json:"count,omitempty"`
	Done      int      `json:"done,omitempty"`
	Distilled bool     `json:"distilled,omitempty"`
}

// StepFromEvent copies the fields a stored trail needs off a live research
// event. Other event types are ignored by the collector.
func StepFromEvent(ev ai.ResearchEvent) StoredStep {
	return StoredStep{
		Kind:      ev.Kind,
		Status:    ev.Status,
		Query:     ev.Query,
		Titles:    ev.Titles,
		Count:     ev.Count,
		Done:      ev.Done,
		Distilled: ev.Distilled,
	}
}

func EncodeSteps(steps []StoredStep) types.JSONRaw {
	if len(steps) == 0 {
		return nil
	}
	if len(steps) > MaxStepsPerTurn {
		steps = steps[len(steps)-MaxStepsPerTurn:]
	}
	encoded, err := json.Marshal(steps)
	if err != nil {
		return nil
	}
	for len(encoded) > MaxStepsJSONBytes && len(steps) > 1 {
		steps = steps[1:]
		encoded, err = json.Marshal(steps)
		if err != nil {
			return nil
		}
	}
	if len(encoded) > MaxStepsJSONBytes {
		return nil
	}
	return types.JSONRaw(encoded)
}

// EncodeToolCalls stores what an assistant turn asked the tools for. Over
// budget it stores nothing rather than a prefix: half a call list replays as a
// call nothing answered, which providers refuse outright.
func EncodeToolCalls(calls []ai.ToolCall) types.JSONRaw {
	if len(calls) == 0 {
		return nil
	}
	encoded, err := json.Marshal(calls)
	if err != nil || len(encoded) > MaxToolCallsJSONBytes {
		return nil
	}
	return types.JSONRaw(encoded)
}

func DecodeToolCalls(record *core.Record) []ai.ToolCall {
	if !hasToolCalls(record) {
		return nil
	}
	raw := strings.TrimSpace(record.GetString("tool_calls"))
	var calls []ai.ToolCall
	if err := json.Unmarshal([]byte(raw), &calls); err != nil {
		return nil
	}
	return calls
}

// EncodeUsage stores what the turn cost in context. Nil for a turn that
// reported nothing, so an older transcript and a turn on a provider that counts
// nothing read the same way: no line rather than a zero.
func EncodeUsage(usage ai.TurnUsage) types.JSONRaw {
	if usage.Empty() {
		return nil
	}
	encoded, err := json.Marshal(usage)
	if err != nil || len(encoded) > MaxUsageJSONBytes {
		return nil
	}
	return types.JSONRaw(encoded)
}

func DecodeUsage(record *core.Record) *ai.TurnUsage {
	raw := strings.TrimSpace(record.GetString("usage"))
	if raw == "" || raw == "null" {
		return nil
	}
	var usage ai.TurnUsage
	if err := json.Unmarshal([]byte(raw), &usage); err != nil {
		return nil
	}
	return &usage
}

func DecodeSteps(record *core.Record) []StoredStep {
	raw := strings.TrimSpace(record.GetString("steps"))
	if raw == "" || raw == "null" {
		return nil
	}
	var steps []StoredStep
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		return nil
	}
	return steps
}

// DecodeHits reads the hits back off a message record. PocketBase hands a JSON
// field back typed after a save and as a raw string after a fresh read, the
// polymorphism models.PeopleOrOrganizations documents.
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

type SessionInfo struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Mode is the search mode the last turn ran in ("" for document chats).
	Mode string `json:"mode,omitempty"`
	// Provider and Model are the binding the conversation runs on, empty when
	// it runs on the one in Settings, so reopening a chat restores the picker.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Document is set for KindDocument sessions; DocumentTitle is filled by the
	// handler, which is the layer that may read the documents collection.
	Document      string `json:"document,omitempty"`
	DocumentTitle string `json:"document_title,omitempty"`
	MessageCount  int    `json:"message_count"`
	LastMessageAt string `json:"last_message_at"`
	Created       string `json:"created"`
	Updated       string `json:"updated"`
}

type MessageInfo struct {
	ID         string           `json:"id"`
	Seq        int              `json:"seq"`
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	RunID      string           `json:"run_id,omitempty"`
	Documents  []ai.DocumentHit `json:"documents,omitempty"`
	Steps      []StoredStep     `json:"steps,omitempty"`
	Usage      *ai.TurnUsage    `json:"usage,omitempty"`
	Incomplete bool             `json:"incomplete,omitempty"`
	Created    string           `json:"created"`
}

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
		Provider:      record.GetString("provider"),
		Model:         record.GetString("model"),
		Document:      record.GetString("document"),
		MessageCount:  record.GetInt("message_count"),
		LastMessageAt: lastMessageAt,
		Created:       record.GetDateTime("created").String(),
		Updated:       record.GetDateTime("updated").String(),
	}
}

// BindingOf reads the provider and model a conversation is pinned to. A nil
// record answers the same as an unpinned one, which is what a conversation that
// does not exist yet needs.
func BindingOf(record *core.Record) aiprovider.Binding {
	if record == nil {
		return aiprovider.Binding{}
	}
	return aiprovider.Binding{
		ProviderID: record.GetString("provider"),
		Model:      record.GetString("model"),
	}.Normalized()
}

// VisibleMessages is the conversation a person reads, folded out of the
// provider array a research turn stores. The tool calls and their results are
// not messages; they are how an answer was arrived at, so they ride on it as
// its trail. A turn whose run died has a trail and no answer to hang it on, and
// it rides on the question instead -- that is what makes an interrupted turn
// look like one still in progress rather than like nothing at all.
func VisibleMessages(records []*core.Record) []MessageInfo {
	messages := make([]MessageInfo, 0, len(records))
	var pending []StoredStep
	for _, record := range records {
		info := ToMessageInfo(record)
		if !VisibleRecord(record) {
			pending = append(pending, info.Steps...)
			continue
		}
		if len(pending) > 0 && info.Role == RoleAssistant {
			info.Steps = append(pending, info.Steps...)
			pending = nil
		}
		messages = append(messages, info)
	}
	if len(pending) > 0 && len(messages) > 0 {
		last := &messages[len(messages)-1]
		last.Steps = append(last.Steps, pending...)
	}
	return messages
}

func ToMessageInfo(record *core.Record) MessageInfo {
	return MessageInfo{
		ID:         record.Id,
		Seq:        record.GetInt("seq"),
		Role:       record.GetString("role"),
		Content:    record.GetString("content"),
		RunID:      record.GetString("run_id"),
		Documents:  DecodeHits(record),
		Steps:      DecodeSteps(record),
		Usage:      DecodeUsage(record),
		Incomplete: record.GetBool("incomplete"),
		Created:    record.GetDateTime("created").String(),
	}
}
