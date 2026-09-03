package appapi

import (
	"context"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chat"
)

// sessionRecord is a chat_sessions record with an id, without a database.
func sessionRecord(id string) *core.Record {
	record := core.NewRecord(core.NewBaseCollection(chat.SessionsCollection))
	record.Id = id
	return record
}

func TestSearchTurnAgentContextNamesTheConversation(t *testing.T) {
	turn := searchTurn{session: sessionRecord("conv123")}
	if got := aiprovider.SessionFrom(turn.agentContext(context.Background())); got != "conv123" {
		t.Errorf("agentContext session = %q, want %q", got, "conv123")
	}
}
