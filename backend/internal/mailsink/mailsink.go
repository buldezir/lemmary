package mailsink

import (
	"fmt"
	"net/mail"
	"sort"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/mailer"
)

const CollectionName = "outbound_emails"

// Register replaces the sendmail fallback: when SMTP is disabled, outbound
// messages are stored in the outbound_emails collection instead of being sent.
func Register(app core.App) {
	app.OnMailerSend().BindFunc(func(e *core.MailerEvent) error {
		if e.App.Settings().SMTP.Enabled {
			return e.Next()
		}
		if err := Persist(e.App, e.Message); err != nil {
			return err
		}
		// Skip sendmail (do not call e.Next()).
		return nil
	})
}

// Persist writes a mailer message to the outbound_emails collection.
func Persist(app core.App, message *mailer.Message) error {
	if message == nil {
		return fmt.Errorf("mailsink: nil message")
	}

	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return fmt.Errorf("mailsink: find collection: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("from_address", message.From.Address)
	record.Set("from_name", message.From.Name)
	record.Set("to", addressesJSON(message.To))
	record.Set("cc", addressesJSON(message.Cc))
	record.Set("bcc", addressesJSON(message.Bcc))
	record.Set("subject", message.Subject)
	record.Set("html", message.HTML)
	record.Set("text", message.Text)
	record.Set("headers", headersOrEmpty(message.Headers))
	record.Set("attachment_names", attachmentNames(message))

	if err := app.Save(record); err != nil {
		return fmt.Errorf("mailsink: save: %w", err)
	}
	return nil
}

type addressDTO struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

func addressesJSON(addrs []mail.Address) any {
	if len(addrs) == 0 {
		return []addressDTO{}
	}
	out := make([]addressDTO, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, addressDTO{Name: a.Name, Address: a.Address})
	}
	return out
}

func headersOrEmpty(headers map[string]string) any {
	if headers == nil {
		return map[string]string{}
	}
	return headers
}

func attachmentNames(message *mailer.Message) any {
	names := make([]string, 0, len(message.Attachments)+len(message.InlineAttachments))
	for name := range message.Attachments {
		names = append(names, name)
	}
	for name := range message.InlineAttachments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
