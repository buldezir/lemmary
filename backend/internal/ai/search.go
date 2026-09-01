package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

const (
	// maxSearchToolRounds is the whole of Search mode: expand the request into
	// keywords, run the lookups, answer. Questions that need more than one pass
	// belong in Research mode, which is bounded by the context window instead.
	maxSearchToolRounds = 1

	// maxToolResultBytes caps the JSON fed back to the model per tool call.
	maxToolResultBytes = 24000
)

// Passage is a verbatim slice of a document's text, quoted by a search hit.
// Page is filled only when the extraction preserved page boundaries, which no
// current OCR provider does.
type Passage struct {
	Page int    `json:"page,omitempty"`
	Text string `json:"text"`
}

type DocumentHit struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	DocumentDate string `json:"document_date,omitempty"`
	Summary      string `json:"summary,omitempty"`
	// OCRSnippet is the first passage shortened for display. Kept filled even
	// when Passages is set: it is what the stored turn and the result card
	// show, and neither wants three paragraphs.
	OCRSnippet string `json:"ocr_snippet,omitempty"`
	// Passages are the verbatim pieces of the document that matched. One to
	// three of them: enough that a hit is evidence rather than a filename,
	// few enough that a result list is not a read.
	Passages      []Passage `json:"passages,omitempty"`
	DocumentType  string    `json:"document_type,omitempty"`
	Correspondent string    `json:"correspondent,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
}

type SearchDocumentsArgs struct {
	Query         string   `json:"query"`
	DateFrom      string   `json:"date_from,omitempty"`
	DateTo        string   `json:"date_to,omitempty"`
	DocumentType  string   `json:"document_type,omitempty"`
	Correspondent string   `json:"correspondent,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Limit         int      `json:"limit,omitempty"`
}

// decodeSearchArgs parses tool-call arguments, coercing scalar-kind mismatches
// (a numeric query, a stringified limit) instead of dropping the whole call —
// models routinely get JSON scalar types wrong, especially via the DSML path.
func decodeSearchArgs(data string) (SearchDocumentsArgs, error) {
	var args SearchDocumentsArgs
	if err := json.Unmarshal([]byte(data), &args); err == nil {
		return args, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return args, err
	}
	return SearchDocumentsArgs{
		Query:         coerceString(raw["query"]),
		DateFrom:      coerceString(raw["date_from"]),
		DateTo:        coerceString(raw["date_to"]),
		DocumentType:  coerceString(raw["document_type"]),
		Correspondent: coerceString(raw["correspondent"]),
		Tags:          coerceStringSlice(raw["tags"]),
		Limit:         coerceInt(raw["limit"]),
	}, nil
}

func coerceString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

func coerceStringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := coerceString(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{t}
	default:
		return nil
	}
}

func coerceInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	default:
		return 0
	}
}

// DocumentSearcher runs a user-scoped keyword search against the document archive.
type DocumentSearcher func(ctx context.Context, args SearchDocumentsArgs) ([]DocumentHit, error)

type SearchAgent interface {
	// Search finds documents and answers from their metadata and snippets.
	Search(ctx context.Context, messages []ChatMessage, availableTags []string, search DocumentSearcher) (reply string, hits []DocumentHit, err error)

	// Research reads the documents it finds and writes a cited answer,
	// reporting each step through emit as it goes.
	Research(ctx context.Context, req ResearchRequest, emit func(ResearchEvent)) (ResearchResult, error)
}

type openAISearchAgent struct {
	client         *OpenAIClient
	languages      string
	resultLanguage string
	contextTokens  int
}

func NewSearchAgent(sdk, apiKey, model, baseURL string, timeout time.Duration, languages, resultLanguage string, contextTokens int, logger *slog.Logger) SearchAgent {
	return &openAISearchAgent{
		client:         NewOpenAIClient(sdk, apiKey, model, baseURL, "", "", timeout, logger),
		languages:      strings.TrimSpace(languages),
		resultLanguage: strings.TrimSpace(resultLanguage),
		contextTokens:  contextTokens,
	}
}

func (a *openAISearchAgent) Search(ctx context.Context, messages []ChatMessage, availableTags []string, search DocumentSearcher) (string, []DocumentHit, error) {
	if a.client.apiKey == "" {
		return "", nil, fmt.Errorf("AI API key is not configured")
	}
	if search == nil {
		return "", nil, fmt.Errorf("document searcher is required")
	}

	maxRounds := maxSearchToolRounds

	apiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1+maxRounds*4)
	apiMessages = append(apiMessages, openai.SystemMessage(buildSearchSystemPrompt(a.languages, a.resultLanguage, availableTags)))
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if role == "" || content == "" {
			continue
		}
		if role != "user" && role != "assistant" {
			return "", nil, fmt.Errorf("invalid message role: %s", role)
		}
		if role == "user" {
			apiMessages = append(apiMessages, openai.UserMessage(content))
		} else {
			apiMessages = append(apiMessages, openai.AssistantMessage(content))
		}
	}
	if len(apiMessages) < 2 {
		return "", nil, fmt.Errorf("at least one user message is required")
	}

	tools := searchDocumentsTools()
	allHits := make([]DocumentHit, 0)
	seenIDs := map[string]struct{}{}

	for round := 0; round <= maxRounds; round++ {
		allowTools := round < maxRounds
		params := openai.ChatCompletionNewParams{
			Model:       shared.ChatModel(a.client.model),
			Messages:    apiMessages,
			Temperature: CompletionTemperature(a.client.model, 0.2),
		}
		// Tools stay declared on every round: OpenAI-compatible endpoints reject
		// a bare tool_choice with no tools array, which would 400 the final round.
		params.Tools = tools
		if allowTools {
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String("auto"),
			}
		} else {
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String("none"),
			}
		}

		requestStart := time.Now()
		chatResp, err := a.client.complete(ctx, params,
			"purpose", "search",
			"round", round,
			"allow_tools", allowTools,
			"messages", len(apiMessages),
		)
		if err != nil {
			a.client.logger.Error("search agent request failed",
				"duration", time.Since(requestStart).Round(time.Millisecond),
				slog.Any("error", err),
			)
			return "", nil, fmt.Errorf("openai search completion: %w", err)
		}
		a.client.logger.Info("search agent response",
			"choices", len(chatResp.Choices),
			"duration", time.Since(requestStart).Round(time.Millisecond),
		)
		if len(chatResp.Choices) == 0 {
			return "", nil, fmt.Errorf("openai returned no choices")
		}

		msg := chatResp.Choices[0].Message
		nativeCalls := msg.ToolCalls
		dsmlCalls := []parsedToolCall(nil)
		if len(nativeCalls) == 0 {
			dsmlCalls = parseDSMLToolCalls(msg.Content)
		}

		hasToolCalls := len(nativeCalls) > 0 || len(dsmlCalls) > 0

		// Final round, or model produced a plain answer: return user-facing text only.
		if !allowTools || !hasToolCalls {
			a.client.logger.Info("search agent finalizing",
				"allow_tools", allowTools,
				"dsml", contentHasDSMLToolCalls(msg.Content),
				"content_chars", len(msg.Content),
				"hits", len(allHits),
			)
			reply := finalizeSearchReply(msg.Content, allHits)
			// If the model ignored "no tools" and emitted DSML again, force one more
			// answer-only turn when we still have search hits to ground it.
			if !allowTools && contentHasDSMLToolCalls(msg.Content) && round == maxRounds {
				forced, forcedHits, err := a.forceFinalAnswer(ctx, apiMessages, allHits)
				if err == nil && strings.TrimSpace(forced) != "" && !replyLooksLikeToolMarkup(forced) {
					return forced, forcedHits, nil
				}
			}
			return reply, allHits, nil
		}

		results := make([]toolExecResult, 0)

		if len(nativeCalls) > 0 {
			apiMessages = append(apiMessages, msg.ToParam())
			for _, call := range nativeCalls {
				result := a.executeToolCall(ctx, search, call.ID, call.Function.Name, call.Function.Arguments, &allHits, seenIDs)
				results = append(results, result)
				apiMessages = append(apiMessages, openai.ToolMessage(result.Content, call.ID))
			}
		} else {
			// DSML models put tool calls in content; feed results back as a user message.
			apiMessages = append(apiMessages, openai.AssistantMessage(msg.Content))
			for _, call := range dsmlCalls {
				result := a.executeToolCall(ctx, search, call.ID, call.Name, call.Arguments, &allHits, seenIDs)
				results = append(results, result)
			}
			apiMessages = append(apiMessages, openai.UserMessage(formatDSMLToolResults(results)))
		}
	}

	return finalizeSearchReply("", allHits), allHits, nil
}

func (a *openAISearchAgent) forceFinalAnswer(
	ctx context.Context,
	apiMessages []openai.ChatCompletionMessageParamUnion,
	hits []DocumentHit,
) (string, []DocumentHit, error) {
	msgs := append([]openai.ChatCompletionMessageParamUnion{}, apiMessages...)
	msgs = append(msgs, openai.UserMessage(
		`Stop. Do not call any tools and do not output DSML/tool markup.
Write the final answer for the user in natural language only, based on the tool results already provided.
If nothing relevant was found, say so clearly.`,
	))

	requestStart := time.Now()
	chatResp, err := a.client.complete(ctx, openai.ChatCompletionNewParams{
		Model:       shared.ChatModel(a.client.model),
		Messages:    msgs,
		Temperature: CompletionTemperature(a.client.model, 0.2),
	}, "purpose", "search_final", "messages", len(msgs))
	if err != nil {
		a.client.logger.Error("search agent force-final failed",
			"duration", time.Since(requestStart).Round(time.Millisecond),
			slog.Any("error", err),
		)
		return "", hits, err
	}
	if len(chatResp.Choices) == 0 {
		return "", hits, fmt.Errorf("openai returned no choices")
	}
	return finalizeSearchReply(chatResp.Choices[0].Message.Content, hits), hits, nil
}

func finalizeSearchReply(content string, hits []DocumentHit) string {
	reply := stripDSMLMarkup(strings.TrimSpace(content))
	// Defense in depth: drop any leftover DSML-looking markup.
	if contentHasDSMLToolCalls(reply) {
		reply = stripDSMLMarkup(reply)
	}
	if contentHasDSMLToolCalls(reply) || replyLooksLikeToolMarkup(reply) {
		reply = ""
	}
	if reply != "" {
		return reply
	}
	return synthesizeSearchReply(hits)
}

func replyLooksLikeToolMarkup(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "search_documents") &&
		(strings.Contains(lower, "invoke") || strings.Contains(lower, "tool_calls") || strings.Contains(lower, "dsml"))
}

func synthesizeSearchReply(hits []DocumentHit) string {
	if len(hits) == 0 {
		return "No matching documents were found. Try different keywords, or switch to Research mode to have the archive read and analysed."
	}
	var b strings.Builder
	b.WriteString("Here are the documents I found:\n\n")
	for i, hit := range hits {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("\n…and %d more.", len(hits)-10))
			break
		}
		title := strings.TrimSpace(hit.Title)
		if title == "" {
			title = "Untitled document"
		}
		b.WriteString(fmt.Sprintf("- **%s**", title))
		meta := make([]string, 0, 2)
		if hit.DocumentDate != "" {
			meta = append(meta, hit.DocumentDate)
		}
		if hit.DocumentType != "" {
			meta = append(meta, hit.DocumentType)
		}
		if len(meta) > 0 {
			b.WriteString(" (" + strings.Join(meta, " · ") + ")")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (a *openAISearchAgent) executeToolCall(
	ctx context.Context,
	search DocumentSearcher,
	callID, name, argumentsJSON string,
	allHits *[]DocumentHit,
	seenIDs map[string]struct{},
) toolExecResult {
	if name != "search_documents" {
		return toolExecResult{
			ID:      callID,
			Name:    name,
			Content: fmt.Sprintf(`{"error":"unknown tool: %s"}`, name),
		}
	}

	args, err := decodeSearchArgs(argumentsJSON)
	if err != nil {
		return toolExecResult{
			ID:      callID,
			Name:    name,
			Content: `{"error":"invalid tool arguments"}`,
		}
	}

	hits, err := search(ctx, args)
	if err != nil {
		return toolExecResult{
			ID:      callID,
			Name:    name,
			Content: fmt.Sprintf(`{"error":%q}`, err.Error()),
		}
	}

	for _, hit := range hits {
		if hit.ID == "" {
			continue
		}
		if _, ok := seenIDs[hit.ID]; ok {
			continue
		}
		seenIDs[hit.ID] = struct{}{}
		*allHits = append(*allHits, hit)
	}

	// The same encoder Research uses. Search has no running context budget, so
	// the whole per-call cap is what it may spend -- but it goes through the
	// fit ladder rather than slicing the encoded JSON, which is what this code
	// used to do and which handed the model a payload that was not JSON.
	content, err := encodeSearchResults(hits, maxToolResultBytes)
	if err != nil {
		return toolExecResult{
			ID:      callID,
			Name:    name,
			Content: `{"error":"failed to encode search results"}`,
		}
	}
	return toolExecResult{ID: callID, Name: name, Content: content}
}

func buildSearchSystemPrompt(languages, resultLanguage string, availableTags []string) string {
	var b strings.Builder
	b.WriteString(`You help the user find documents in their personal archive.
The user may ask in broad natural language that keyword search alone cannot handle.
Use the search_documents tool to look up documents. Expand the request into concrete keywords and filters.
Search bilingual metadata (title/purpose/summary and their *_original fields) plus OCR text.
Prefer precise date_from/date_to, document_type, correspondent, or tags filters when the query implies them.
When filtering by tags, use exact names from the available archive tags list below — never invent tag names.
Cite real document ids and titles from tool results only. Never invent documents.
If nothing relevant is found, say so clearly and suggest alternative search terms.
Be concise. Answer in the same language as the user's latest message.
Never output tool markup, DSML tags, or raw function-call XML in your final answer — only natural language.
You have one round of tool calls: gather everything you need with search_documents, then answer.
Each result carries verbatim passages from the document. When a passage literally contains the answer, give it directly and cite the document.
When answering would need more of a document than the passages show, say what you found and suggest Research mode, which reads the documents.
`)

	b.WriteString(formatAvailableTagsPrompt(availableTags))
	b.WriteString(formatLanguagePrompt(languages, resultLanguage))

	return b.String()
}

// formatLanguagePrompt tells the agent which languages to expand keywords into.
// Shared by Search and Research: recall across a multilingual archive depends on
// the same translation step in both.
func formatLanguagePrompt(languages, resultLanguage string) string {
	if languages != "" {
		return fmt.Sprintf(`
Always try keyword searches across these archive languages (translate key terms as needed): %s.
Call search_documents multiple times when useful — once per language or synonym set.
`, languages)
	}
	var b strings.Builder
	b.WriteString(`
No fixed archive language list is configured. Expand keywords into the language of the user's query`)
	if resultLanguage != "" {
		b.WriteString(fmt.Sprintf(` and into %s`, resultLanguage))
	}
	b.WriteString(`. Call search_documents multiple times when useful.
`)
	return b.String()
}

func formatAvailableTagsPrompt(tags []string) string {
	cleaned := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, tag)
	}
	if len(cleaned) == 0 {
		return `
Available archive tags: none are defined yet. Do not pass a tags filter.
`
	}
	return fmt.Sprintf(`
Available archive tags (pass exact names via the tags filter when relevant): %s.
`, strings.Join(cleaned, ", "))
}

func searchDocumentsTools() []openai.ChatCompletionToolParam {
	return []openai.ChatCompletionToolParam{{
		Function: shared.FunctionDefinitionParam{
			Name: "search_documents",
			Description: openai.String("Search the user's document archive by meaning and by keywords, with optional filters. " +
				"Returns matching documents with 1-3 verbatim passages from each."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type": "string",
						"description": "What to look for, as keywords or a short phrase. " +
							"Matched against titles, summaries and OCR text; not every word has to occur, so describe the thing rather than guessing its exact wording.",
					},
					"date_from": map[string]any{
						"type":        "string",
						"description": "Inclusive lower bound for document_date (YYYY-MM-DD).",
					},
					"date_to": map[string]any{
						"type":        "string",
						"description": "Inclusive upper bound for document_date (YYYY-MM-DD).",
					},
					"document_type": map[string]any{
						"type":        "string",
						"description": "Optional document type name filter (substring match).",
					},
					"correspondent": map[string]any{
						"type":        "string",
						"description": "Optional correspondent name filter (substring match).",
					},
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional tag name filters. Use exact names from the available archive tags list. Matches documents that have any of these tags.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results to return (1-20). Default 10.",
					},
				},
				"required": []string{"query"},
			},
		},
	}}
}
