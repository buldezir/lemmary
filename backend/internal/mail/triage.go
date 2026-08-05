package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// ImportDecision is one message the AI wants imported.
type ImportDecision struct {
	MessageID string   `json:"message_id"`
	Filenames []string `json:"filenames"`
}

type triageResponse struct {
	Import []ImportDecision `json:"import"`
}

// Triager decides which attachments to import from a batch of messages.
type Triager interface {
	Triage(ctx context.Context, mode string, messages []*Message) ([]ImportDecision, error)
}

// AITriager uses an OpenAI-compatible chat model for triage.
type AITriager struct {
	APIKey  string
	Model   string
	BaseURL string
}

func NewAITriager(apiKey, model, baseURL string) *AITriager {
	return &AITriager{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
	}
}

func (t *AITriager) Triage(ctx context.Context, mode string, messages []*Message) ([]ImportDecision, error) {
	if t == nil || strings.TrimSpace(t.APIKey) == "" {
		return nil, fmt.Errorf("OpenAI API key is not configured")
	}
	if len(messages) == 0 {
		return nil, nil
	}

	opts := []option.RequestOption{option.WithAPIKey(t.APIKey)}
	if t.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(t.BaseURL))
	}
	client := openai.NewClient(opts...)

	userPayload, err := json.Marshal(buildTriagePayload(mode, messages))
	if err != nil {
		return nil, err
	}

	chatResp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: shared.ChatModel(t.Model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(triageSystemPrompt(mode)),
			openai.UserMessage(string(userPayload)),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
		Temperature: openai.Float(0.1),
	})
	if err != nil {
		return nil, fmt.Errorf("triage chat completion: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("triage returned no choices")
	}
	return ParseTriageResponse(chatResp.Choices[0].Message.Content)
}

func triageSystemPrompt(mode string) string {
	base := `You triage email messages to decide which attachments should be imported into a document archive.
Prefer invoices, receipts, bills, tax documents, contracts, and similar fileable documents.
Skip noise: signature images, logos, tracking pixels, calendars (.ics), vCards, tiny images, and unrelated marketing PDFs when the subject/body make that clear.
Return ONLY valid JSON: {"import":[{"message_id":"...","filenames":["file.pdf"]}]}
If nothing should be imported, return {"import":[]}.
Only list filenames that appear on the message. If multiple attachments, include only those worth importing.`
	if mode == ModeDeep {
		base += `
You are given subject, from, date, attachment filenames, and email body text. If unsure, lean toward skip unless the body clearly indicates a document to file.`
	} else {
		base += `
You are given subject, from, date, and attachment filenames only (no body). Be conservative when the subject/filenames are ambiguous.`
	}
	return base
}

func buildTriagePayload(mode string, messages []*Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		filenames := make([]string, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			filenames = append(filenames, a.Filename)
		}
		item := map[string]any{
			"message_id": m.ID,
			"subject":    m.Subject,
			"from":       m.From,
			"date":       m.Date,
			"filenames":  filenames,
		}
		if mode == ModeDeep {
			item["body"] = m.BodyText
		}
		out = append(out, item)
	}
	return out
}

// ParseTriageResponse parses the model JSON into import decisions.
func ParseTriageResponse(content string) ([]ImportDecision, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty triage response")
	}
	var resp triageResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return nil, fmt.Errorf("parse triage json: %w", err)
	}
	out := make([]ImportDecision, 0, len(resp.Import))
	for _, d := range resp.Import {
		id := strings.TrimSpace(d.MessageID)
		if id == "" {
			continue
		}
		names := make([]string, 0, len(d.Filenames))
		seen := map[string]struct{}{}
		for _, f := range d.Filenames {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if _, ok := seen[f]; ok {
				continue
			}
			seen[f] = struct{}{}
			names = append(names, f)
		}
		if len(names) == 0 {
			continue
		}
		out = append(out, ImportDecision{MessageID: id, Filenames: names})
	}
	return out, nil
}