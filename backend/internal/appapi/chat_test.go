package appapi

import (
	"strings"
	"testing"
	"unicode/utf8"

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

// There is no length cap: what a long question costs is the model's context,
// which the composer warns about, not a refusal here.
func TestValidateChatContentAcceptsLong(t *testing.T) {
	long := strings.Repeat("щ", 200000)
	got, err := validateChatContent(long)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != long {
		t.Fatalf("content was altered: %d runes in, %d out",
			utf8.RuneCountInString(long), utf8.RuneCountInString(got))
	}
}

func TestValidateRunIDTrimsAndBounds(t *testing.T) {
	got, err := validateRunID("  run-123  ")
	if err != nil || got != "run-123" {
		t.Fatalf("validateRunID = %q, %v", got, err)
	}
	if _, err := validateRunID(strings.Repeat("r", chat.MaxRunIDRunes+1)); err == nil {
		t.Fatal("expected an error for an oversized run id")
	}
}

func TestParseSearchMode(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"research", chat.ModeResearch},
		{"  RESEARCH  ", chat.ModeResearch},
		{"search", chat.ModeSearch},
		{"", chat.ModeSearch},
		// The modes an older client would send. Both are plain search now.
		{"deep", chat.ModeSearch},
		{"shallow", chat.ModeSearch},
		{"nonsense", chat.ModeSearch},
	} {
		if got := parseSearchMode(tc.raw); got != tc.want {
			t.Errorf("parseSearchMode(%q) = %q want %q", tc.raw, got, tc.want)
		}
	}
}
