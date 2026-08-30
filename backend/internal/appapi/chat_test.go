package appapi

import (
	"strconv"
	"strings"
	"testing"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chat"
)

func TestValidateChatContentTrims(t *testing.T) {
	got, err := validateChatContent("  what is the total?  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "what is the total?" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateChatContentRejectsBlank(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\t"} {
		if _, err := validateChatContent(raw); err == nil {
			t.Errorf("expected an error for %q", raw)
		}
	}
}

// Rejected rather than truncated, and the message says the number so the user
// knows how much to cut.
func TestValidateChatContentRejectsOversized(t *testing.T) {
	_, err := validateChatContent(strings.Repeat("x", chat.MaxUserContentRunes+1))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(chat.MaxUserContentRunes)) {
		t.Fatalf("error should name the limit: %v", err)
	}
}

// The cap counts runes, not bytes: a multi-byte message at the limit is fine.
func TestValidateChatContentCountsRunes(t *testing.T) {
	if _, err := validateChatContent(strings.Repeat("щ", chat.MaxUserContentRunes)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSearchMode(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want ai.SearchMode
	}{
		{"deep", ai.SearchModeDeep},
		{"  DEEP  ", ai.SearchModeDeep},
		{"shallow", ai.SearchModeShallow},
		{"", ai.SearchModeShallow},
		{"nonsense", ai.SearchModeShallow},
	} {
		if got := parseSearchMode(tc.raw); got != tc.want {
			t.Errorf("parseSearchMode(%q) = %q want %q", tc.raw, got, tc.want)
		}
	}
}
