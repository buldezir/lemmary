package chat_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chat"
)

func TestParseKind(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want chat.Kind
		ok   bool
	}{
		{"search", chat.KindSearch, true},
		{"document", chat.KindDocument, true},
		{"  Search  ", chat.KindSearch, true},
		{"", "", false},
		{"other", "", false},
	} {
		got, ok := chat.ParseKind(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseKind(%q) = %q,%v want %q,%v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDeriveTitleCollapsesWhitespace(t *testing.T) {
	got := chat.DeriveTitle("  find the\n\tplumbing   invoice  ")
	if got != "find the plumbing invoice" {
		t.Fatalf("got %q", got)
	}
}

func TestDeriveTitleTruncates(t *testing.T) {
	got := chat.DeriveTitle(strings.Repeat("a", 200))
	if utf8.RuneCountInString(got) != chat.MaxTitleRunes+1 {
		t.Fatalf("expected %d runes plus an ellipsis, got %d: %q", chat.MaxTitleRunes, utf8.RuneCountInString(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected an ellipsis, got %q", got)
	}
}

// A multi-byte title must be cut on a rune boundary, not a byte one.
func TestDeriveTitleMultiByte(t *testing.T) {
	got := chat.DeriveTitle(strings.Repeat("щ", 200))
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != chat.MaxTitleRunes+1 {
		t.Fatalf("got %d runes: %q", utf8.RuneCountInString(got), got)
	}
}

func TestDeriveTitleBlankFallsBack(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\t "} {
		if got := chat.DeriveTitle(raw); got != chat.UntitledSession {
			t.Errorf("DeriveTitle(%q) = %q want %q", raw, got, chat.UntitledSession)
		}
	}
}

func TestNormalizeTitle(t *testing.T) {
	if got := chat.NormalizeTitle("  Tax   2024 "); got != "Tax 2024" {
		t.Fatalf("got %q", got)
	}
	if got := chat.NormalizeTitle("   "); got != chat.UntitledSession {
		t.Fatalf("blank rename should fall back, got %q", got)
	}
}

// The ellipsis TruncateRunes appends is part of the result, so a value cut to
// exactly the column width would be one rune too long to save. Everything
// written to a bounded column goes through FitColumn for that reason, and this
// is the assertion that keeps it honest.
func TestFitColumnLeavesRoomForTheEllipsis(t *testing.T) {
	for _, max := range []int{1, 2, 10, chat.MaxTitleColumnRunes, chat.MaxMessageRunes} {
		got := chat.FitColumn(strings.Repeat("b", max*2), max)
		if n := utf8.RuneCountInString(got); n > max {
			t.Errorf("FitColumn(_, %d) returned %d runes", max, n)
		}
	}
}

func TestNormalizeTitleFitsTheColumn(t *testing.T) {
	long := chat.NormalizeTitle(strings.Repeat("b", 500))
	if n := utf8.RuneCountInString(long); n > chat.MaxTitleColumnRunes {
		t.Fatalf("rename exceeded the column: %d runes", n)
	}
}

// A derived title has to fit too, and it is cut to the shorter display length
// rather than the column width.
func TestDeriveTitleFitsTheColumn(t *testing.T) {
	long := chat.DeriveTitle(strings.Repeat("c", 500))
	if n := utf8.RuneCountInString(long); n > chat.MaxTitleColumnRunes {
		t.Fatalf("derived title exceeded the column: %d runes", n)
	}
}

func msg(role, content string) ai.ChatMessage {
	return ai.ChatMessage{Role: role, Content: content}
}

func TestClampHistoryEmpty(t *testing.T) {
	got := chat.ClampHistory(nil)
	if got == nil {
		t.Fatal("expected a non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("got %d messages", len(got))
	}
}

func TestClampHistoryKeepsShortConversation(t *testing.T) {
	in := []ai.ChatMessage{msg(chat.RoleUser, "a"), msg(chat.RoleAssistant, "b"), msg(chat.RoleUser, "c")}
	got := chat.ClampHistory(in)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
}

func TestClampHistoryCapsMessageCount(t *testing.T) {
	in := make([]ai.ChatMessage, 0, 100)
	for i := 0; i < 100; i++ {
		role := chat.RoleUser
		if i%2 == 1 {
			role = chat.RoleAssistant
		}
		in = append(in, msg(role, "x"))
	}
	got := chat.ClampHistory(in)
	if len(got) > chat.MaxHistoryMessages {
		t.Fatalf("got %d messages, want at most %d", len(got), chat.MaxHistoryMessages)
	}
	if got[len(got)-1].Content != in[len(in)-1].Content {
		t.Fatal("the newest message must survive")
	}
}

func TestClampHistoryCapsRunes(t *testing.T) {
	big := strings.Repeat("x", chat.MaxHistoryRunes/2)
	in := []ai.ChatMessage{
		msg(chat.RoleUser, big),
		msg(chat.RoleAssistant, big),
		msg(chat.RoleUser, big),
		msg(chat.RoleAssistant, big),
		msg(chat.RoleUser, "the question"),
	}
	got := chat.ClampHistory(in)

	total := 0
	for _, m := range got {
		total += utf8.RuneCountInString(m.Content)
	}
	if total > chat.MaxHistoryRunes {
		t.Fatalf("history is %d runes, over the %d budget", total, chat.MaxHistoryRunes)
	}
	if got[len(got)-1].Content != "the question" {
		t.Fatal("the newest message must survive")
	}
}

// The last message is the question being asked. Dropping it would send the
// model a conversation with no request in it.
func TestClampHistoryKeepsOversizedFinalMessage(t *testing.T) {
	in := []ai.ChatMessage{
		msg(chat.RoleUser, "old"),
		msg(chat.RoleAssistant, "older answer"),
		msg(chat.RoleUser, strings.Repeat("y", chat.MaxHistoryRunes*2)),
	}
	got := chat.ClampHistory(in)
	if len(got) != 1 {
		t.Fatalf("got %d messages, want only the final one", len(got))
	}
	if utf8.RuneCountInString(got[0].Content) != chat.MaxHistoryRunes*2 {
		t.Fatal("the final message must not be trimmed")
	}
}

// A window opening on an answer reads as though the user's question had been
// edited out of the conversation.
func TestClampHistoryNeverStartsOnAnAssistantTurn(t *testing.T) {
	big := strings.Repeat("z", chat.MaxHistoryRunes/2)
	in := []ai.ChatMessage{
		msg(chat.RoleUser, big),
		msg(chat.RoleAssistant, big),
		msg(chat.RoleUser, "next question"),
	}
	got := chat.ClampHistory(in)
	if got[0].Role == chat.RoleAssistant {
		t.Fatalf("history starts on an assistant turn: %+v", got)
	}
}

func hit(id string) ai.DocumentHit {
	return ai.DocumentHit{ID: id, Title: "Invoice " + id, OCRSnippet: strings.Repeat("s", 400), Summary: strings.Repeat("u", 400)}
}

func hitsRecord(t *testing.T, value any) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection(chat.MessagesCollection)
	collection.Fields.Add(&core.JSONField{Name: "documents", MaxSize: chat.MaxHitsJSONBytes})
	record := core.NewRecord(collection)
	record.Set("documents", value)
	return record
}

func TestEncodeDecodeHitsRoundTrip(t *testing.T) {
	hits := []ai.DocumentHit{hit("a"), hit("b")}
	encoded := chat.EncodeHits(hits)
	if encoded == nil {
		t.Fatal("expected an encoded payload")
	}
	got := chat.DecodeHits(hitsRecord(t, encoded))
	if len(got) != 2 || got[0].ID != "a" || got[1].Title != "Invoice b" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestEncodeHitsEmpty(t *testing.T) {
	if chat.EncodeHits(nil) != nil {
		t.Fatal("no hits should encode to nothing")
	}
}

func TestEncodeHitsCapsCount(t *testing.T) {
	hits := make([]ai.DocumentHit, 0, chat.MaxHitsPerTurn+20)
	for i := 0; i < chat.MaxHitsPerTurn+20; i++ {
		hits = append(hits, ai.DocumentHit{ID: string(rune('a' + i%26)), Title: "t"})
	}
	var decoded []ai.DocumentHit
	if err := json.Unmarshal([]byte(chat.EncodeHits(hits)), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) > chat.MaxHitsPerTurn {
		t.Fatalf("got %d hits, want at most %d", len(decoded), chat.MaxHitsPerTurn)
	}
}

// Over budget, the long free-text fields go before any hit does: losing a
// snippet costs a preview line, losing a hit costs a card the answer names.
func TestEncodeHitsShedsSnippetsBeforeHits(t *testing.T) {
	hits := make([]ai.DocumentHit, 0, chat.MaxHitsPerTurn)
	for i := 0; i < chat.MaxHitsPerTurn; i++ {
		hits = append(hits, ai.DocumentHit{
			ID:         string(rune('a' + i%26)),
			Title:      "Invoice",
			OCRSnippet: strings.Repeat("s", 3000),
			Summary:    strings.Repeat("u", 3000),
		})
	}
	encoded := chat.EncodeHits(hits)
	if len(encoded) > chat.MaxHitsJSONBytes {
		t.Fatalf("payload is %d bytes, over the %d cap", len(encoded), chat.MaxHitsJSONBytes)
	}
	var decoded []ai.DocumentHit
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != chat.MaxHitsPerTurn {
		t.Fatalf("hits were dropped before snippets: kept %d of %d", len(decoded), chat.MaxHitsPerTurn)
	}
	if decoded[0].OCRSnippet != "" || decoded[0].Summary != "" {
		t.Fatal("expected snippets and summaries to be shed")
	}
}

// PocketBase hands a JSON field back as a raw string after a fresh read and as
// a typed value after a save. Both have to decode.
func TestDecodeHitsHandlesBothRecordShapes(t *testing.T) {
	raw, err := json.Marshal([]ai.DocumentHit{{ID: "x", Title: "X"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for name, value := range map[string]any{
		"raw string": string(raw),
		"json raw":   types.JSONRaw(raw),
	} {
		got := chat.DecodeHits(hitsRecord(t, value))
		if len(got) != 1 || got[0].ID != "x" {
			t.Errorf("%s: got %+v", name, got)
		}
	}
}

func TestDecodeHitsEmptyRecord(t *testing.T) {
	if got := chat.DecodeHits(hitsRecord(t, nil)); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

// The column has to be able to hold anything the API accepts, and a derived
// title has to fit the column a rename also writes to.
func TestLimitsAreConsistent(t *testing.T) {
	if chat.MaxUserContentRunes >= chat.MaxMessageRunes {
		t.Fatalf("MaxUserContentRunes (%d) must fit inside MaxMessageRunes (%d)",
			chat.MaxUserContentRunes, chat.MaxMessageRunes)
	}
	if chat.MaxTitleRunes >= chat.MaxTitleColumnRunes {
		t.Fatalf("MaxTitleRunes (%d) must fit inside MaxTitleColumnRunes (%d)",
			chat.MaxTitleRunes, chat.MaxTitleColumnRunes)
	}
}
