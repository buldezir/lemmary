package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/chat"
)

// A research turn used to throw its own work away: the tool calls and the
// documents they returned lived only inside one agent loop, so a follow-up
// question arrived with the prose of the last answer and none of the evidence
// behind it, and a run that died left nothing at all.
//
// chat_messages becomes the provider array itself. role gains tool and system;
// content stops being required, because an assistant turn that only calls tools
// has no text, and grows to hold a tool result rather than an answer;
// tool_calls and tool_call_id are what make a stored row replayable.
//
// Existing transcripts are untouched and keep working: a conversation with no
// system row and no tool rows replays exactly as it did.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
		if err != nil {
			return err
		}

		if field, ok := collection.Fields.GetByName("role").(*core.SelectField); ok {
			field.Values = chat.ThreadRoles
		}
		if field, ok := collection.Fields.GetByName("content").(*core.TextField); ok {
			field.Required = false
			field.Max = chat.MaxThreadContentRunes
		}
		if collection.Fields.GetByName("tool_calls") == nil {
			collection.Fields.Add(&core.JSONField{Name: "tool_calls", MaxSize: chat.MaxToolCallsJSONBytes})
		}
		if collection.Fields.GetByName("tool_call_id") == nil {
			collection.Fields.Add(&core.TextField{Name: "tool_call_id", Max: chat.MaxToolCallIDRunes})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
		if err != nil {
			return nil
		}
		// The rows themselves are left alone: dropping the columns is enough to
		// undo the schema, and deleting a conversation's working to satisfy a
		// rollback would lose what this migration exists to keep.
		for _, name := range []string{"tool_calls", "tool_call_id"} {
			if field := collection.Fields.GetByName(name); field != nil {
				collection.Fields.RemoveById(field.GetId())
			}
		}
		if field, ok := collection.Fields.GetByName("role").(*core.SelectField); ok {
			field.Values = []string{chat.RoleUser, chat.RoleAssistant}
		}
		if field, ok := collection.Fields.GetByName("content").(*core.TextField); ok {
			field.Required = true
			field.Max = chat.MaxMessageRunes
		}
		return app.Save(collection)
	})
}
