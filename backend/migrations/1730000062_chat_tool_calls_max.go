package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/chat"
)

// Raises chat_messages.tool_calls from 64 KB to chat.MaxToolCallsJSONBytes. A
// research report is stored as the arguments of the create_pdf call that wrote
// it, and a call list over the cap is not stored at all: the report would be
// linked and then not found, and the call's result left with nothing to answer.
// Set unconditionally, as 1730000034 does.
func init() {
	m.Register(func(app core.App) error {
		return setToolCallsMax(app, chat.MaxToolCallsJSONBytes)
	}, func(app core.App) error {
		return setToolCallsMax(app, 64000)
	})
}

func setToolCallsMax(app core.App, size int64) error {
	collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
	if err != nil {
		return err
	}
	if field, ok := collection.Fields.GetByName("tool_calls").(*core.JSONField); ok {
		field.MaxSize = size
	}
	return app.Save(collection)
}
