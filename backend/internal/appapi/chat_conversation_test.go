package appapi

import (
	"context"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

func TestConversationSessionKeepsAContinuingConversation(t *testing.T) {
	if got := conversationSession("  abc123def456ghi  "); got != "abc123def456ghi" {
		t.Errorf("conversationSession = %q, want the trimmed session id", got)
	}
}

func TestConversationSessionMintsAPocketBaseIDForAFirstTurn(t *testing.T) {
	got := conversationSession("")
	if len(got) != core.DefaultIdLength {
		t.Fatalf("conversationSession(%q) = %q (len %d), want a %d-char record id",
			"", got, len(got), core.DefaultIdLength)
	}
	// It becomes the session record's id, so it has to be a legal one.
	for _, r := range got {
		if !containsRune(core.DefaultIdAlphabet, r) {
			t.Fatalf("conversationSession minted %q, which is outside the record id alphabet", got)
		}
	}
	if again := conversationSession(""); again == got {
		t.Errorf("two first turns got the same id %q", got)
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

func TestSearchTurnAgentContextNamesTheConversation(t *testing.T) {
	turn := searchTurn{conversationID: "conv123"}
	if got := aiprovider.SessionFrom(turn.agentContext(context.Background())); got != "conv123" {
		t.Errorf("agentContext session = %q, want %q", got, "conv123")
	}
}
