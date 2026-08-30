package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"

	"lemmary/backend/internal/strutil"
)

const (
	// maxReadIDsPerCall keeps one read_documents call from splitting the
	// remaining budget so thinly that every document comes back as a stub.
	maxReadIDsPerCall = 20

	// maxStalledRounds ends the research phase when the model keeps calling
	// tools that surface nothing new. This is a stall detector, not a round
	// cap: a run that keeps finding documents is never cut short.
	maxStalledRounds = 3

	// The read result carries JSON scaffolding around the document text: a fixed
	// base plus per-document metadata (title, date, correspondent, tags). Held
	// back from the budget so the reply cannot overshoot it.
	readEnvelopeBaseChars   = 128
	readEnvelopePerDocChars = 200
)

// DocumentContent is a document's full extracted text, as read_documents
// returns it to the model.
type DocumentContent struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	DocumentDate  string   `json:"document_date,omitempty"`
	DocumentType  string   `json:"document_type,omitempty"`
	Correspondent string   `json:"correspondent,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Text          string   `json:"text"`
	Truncated     bool     `json:"truncated,omitempty"`
}

// DocumentReader loads full document text for ids the agent has already seen.
// maxTotalChars is the room left in the context window; implementations divide
// it across the requested ids.
type DocumentReader func(ctx context.Context, ids []string, maxTotalChars int) ([]DocumentContent, error)

type ResearchRequest struct {
	Messages      []ChatMessage
	AvailableTags []string
	Search        DocumentSearcher
	Read          DocumentReader
}

type ResearchResult struct {
	Reply     string
	Documents []DocumentHit
}

// ResearchEvent is one line of the run's visible progress. Types: "step",
// "delta", "documents", "message", "error", "done".
type ResearchEvent struct {
	Type           string        `json:"type"`
	Kind           string        `json:"kind,omitempty"`   // search | read | answer
	Status         string        `json:"status,omitempty"` // start | done
	Query          string        `json:"query,omitempty"`
	Titles         []string      `json:"titles,omitempty"`
	Count          int           `json:"count,omitempty"`
	ContextLeftPct int           `json:"context_left_pct,omitempty"`
	Content        string        `json:"content,omitempty"`
	Documents      []DocumentHit `json:"documents,omitempty"`
	Message        string        `json:"message,omitempty"`
}

type readDocumentsArgs struct {
	IDs []string `json:"ids"`
}

// researchState is everything the loop accumulates across rounds.
type researchState struct {
	budget  *contextBudget
	hits    []DocumentHit
	seenIDs map[string]struct{}
	titles  map[string]string
	read    map[string]struct{}
	ran     map[string]struct{}
}

func (a *openAISearchAgent) Research(ctx context.Context, req ResearchRequest, emit func(ResearchEvent)) (ResearchResult, error) {
	if a.client.apiKey == "" {
		return ResearchResult{}, fmt.Errorf("AI API key is not configured")
	}
	if req.Search == nil || req.Read == nil {
		return ResearchResult{}, fmt.Errorf("document searcher and reader are required")
	}
	if emit == nil {
		emit = func(ResearchEvent) {}
	}

	state := &researchState{
		budget:  newContextBudget(a.contextTokens),
		hits:    make([]DocumentHit, 0),
		seenIDs: map[string]struct{}{},
		titles:  map[string]string{},
		read:    map[string]struct{}{},
		ran:     map[string]struct{}{},
	}

	system := buildResearchSystemPrompt(a.languages, a.resultLanguage, req.AvailableTags)
	state.budget.Add(len(system))

	apiMessages := []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(system)}
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if role == "" || content == "" {
			continue
		}
		if role != "user" && role != "assistant" {
			return ResearchResult{}, fmt.Errorf("invalid message role: %s", role)
		}
		if role == "user" {
			apiMessages = append(apiMessages, openai.UserMessage(content))
		} else {
			apiMessages = append(apiMessages, openai.AssistantMessage(content))
		}
		state.budget.Add(len(content))
	}
	if len(apiMessages) < 2 {
		return ResearchResult{}, fmt.Errorf("at least one user message is required")
	}

	tools := researchTools()
	stalled := 0
	round := 0

	// No round cap: the loop ends when the model is ready, when the context
	// window is spent, or when it stops making progress. Every iteration
	// appends at least an assistant message and a tool result, so the budget
	// is always reached in finite time.
	for {
		if err := ctx.Err(); err != nil {
			return ResearchResult{}, err
		}
		if state.budget.Exhausted() {
			a.client.logger.Info("research budget exhausted", "round", round, "documents", len(state.hits))
			break
		}
		if stalled >= maxStalledRounds {
			a.client.logger.Info("research stalled; answering", "round", round, "documents", len(state.hits))
			break
		}

		requestStart := time.Now()
		chatResp, err := a.client.complete(ctx, openai.ChatCompletionNewParams{
			Model:       shared.ChatModel(a.client.model),
			Messages:    apiMessages,
			Temperature: CompletionTemperature(a.client.model, 0.2),
			Tools:       tools,
			ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String("auto"),
			},
		},
			"purpose", "research",
			"round", round,
			"messages", len(apiMessages),
			"context_left_pct", state.budget.LeftPercent(),
		)
		if err != nil {
			a.client.logger.Error("research request failed",
				"round", round,
				"duration", time.Since(requestStart).Round(time.Millisecond),
				slog.Any("error", err),
			)
			return ResearchResult{}, fmt.Errorf("openai research completion: %w", err)
		}
		if len(chatResp.Choices) == 0 {
			return ResearchResult{}, fmt.Errorf("openai returned no choices")
		}

		msg := chatResp.Choices[0].Message
		nativeCalls := msg.ToolCalls
		dsmlCalls := []parsedToolCall(nil)
		if len(nativeCalls) == 0 {
			dsmlCalls = parseDSMLToolCalls(msg.Content)
		}
		if len(nativeCalls) == 0 && len(dsmlCalls) == 0 {
			// The model has stopped gathering; it is ready to answer.
			break
		}

		progressed := false
		if len(nativeCalls) > 0 {
			apiMessages = append(apiMessages, msg.ToParam())
			state.budget.Add(len(msg.Content) + toolCallChars(nativeCalls))
			for _, call := range nativeCalls {
				result, advanced := a.runResearchTool(ctx, req, state, call.ID, call.Function.Name, call.Function.Arguments, emit)
				progressed = progressed || advanced
				apiMessages = append(apiMessages, openai.ToolMessage(result.Content, call.ID))
				state.budget.Add(len(result.Content))
			}
		} else {
			// DSML models put tool calls in content; feed results back as a user message.
			apiMessages = append(apiMessages, openai.AssistantMessage(msg.Content))
			state.budget.Add(len(msg.Content))
			results := make([]toolExecResult, 0, len(dsmlCalls))
			for _, call := range dsmlCalls {
				result, advanced := a.runResearchTool(ctx, req, state, call.ID, call.Name, call.Arguments, emit)
				progressed = progressed || advanced
				results = append(results, result)
			}
			formatted := formatDSMLToolResults(results)
			apiMessages = append(apiMessages, openai.UserMessage(formatted))
			state.budget.Add(len(formatted))
		}

		if progressed {
			stalled = 0
		} else {
			stalled++
		}
		round++
	}

	emit(ResearchEvent{Type: "step", Kind: "answer", Status: "start"})
	reply, err := a.answerResearch(ctx, apiMessages, emit)
	if err != nil {
		return ResearchResult{}, err
	}

	reply = validateCitations(reply, state.seenIDs)
	if strings.TrimSpace(reply) == "" {
		reply = synthesizeSearchReply(state.hits)
	}
	emit(ResearchEvent{Type: "step", Kind: "answer", Status: "done", Count: len(state.read)})

	return ResearchResult{Reply: reply, Documents: state.hits}, nil
}

// answerResearch is the second phase: one completion with no tools declared, so
// the model cannot emit tool markup and every chunk is safe to stream.
func (a *openAISearchAgent) answerResearch(
	ctx context.Context,
	apiMessages []openai.ChatCompletionMessageParamUnion,
	emit func(ResearchEvent),
) (string, error) {
	msgs := append([]openai.ChatCompletionMessageParamUnion{}, apiMessages...)
	msgs = append(msgs, openai.UserMessage(researchAnswerInstruction))

	params := openai.ChatCompletionNewParams{
		Model:       shared.ChatModel(a.client.model),
		Messages:    msgs,
		Temperature: CompletionTemperature(a.client.model, 0.2),
	}

	emitted := 0
	requestStart := time.Now()
	content, err := a.client.completeStreaming(ctx, params, func(delta string) {
		emitted++
		emit(ResearchEvent{Type: "delta", Content: delta})
	}, "purpose", "research_answer", "messages", len(msgs))

	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if emitted > 0 && strings.TrimSpace(content) != "" {
			// Partial answer already on the wire; keep what arrived rather than
			// replaying a second, different answer over it.
			a.client.logger.Warn("research answer stream ended early",
				"chars", len(content),
				slog.Any("error", err),
			)
			return stripDSMLMarkup(strings.TrimSpace(content)), nil
		}
		a.client.logger.Warn("research answer stream failed; falling back to a blocking call",
			slog.Any("error", err),
		)
		chatResp, fallbackErr := a.client.complete(ctx, params, "purpose", "research_answer_fallback")
		if fallbackErr != nil {
			return "", fmt.Errorf("openai research answer: %w", fallbackErr)
		}
		if len(chatResp.Choices) == 0 {
			return "", fmt.Errorf("openai returned no choices")
		}
		content = chatResp.Choices[0].Message.Content
	}

	a.client.logger.Info("research answer complete",
		"chars", len(content),
		"streamed", emitted > 0,
		"duration", time.Since(requestStart).Round(time.Millisecond),
	)
	return stripDSMLMarkup(strings.TrimSpace(content)), nil
}

// runResearchTool dispatches one tool call and reports whether it advanced the
// run — found a document not seen before, or read text not read before.
func (a *openAISearchAgent) runResearchTool(
	ctx context.Context,
	req ResearchRequest,
	state *researchState,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	switch name {
	case "search_documents":
		return a.runSearchTool(ctx, req, state, callID, name, argumentsJSON, emit)
	case "read_documents":
		return a.runReadTool(ctx, req, state, callID, name, argumentsJSON, emit)
	default:
		return toolExecResult{
			ID:      callID,
			Name:    name,
			Content: fmt.Sprintf(`{"error":"unknown tool: %s"}`, name),
		}, false
	}
}

func (a *openAISearchAgent) runSearchTool(
	ctx context.Context,
	req ResearchRequest,
	state *researchState,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	args, err := decodeSearchArgs(argumentsJSON)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"invalid tool arguments"}`}, false
	}
	if repeat, ok := state.claimCall(name, args); !ok {
		return toolExecResult{ID: callID, Name: name, Content: repeat}, false
	}

	emit(ResearchEvent{Type: "step", Kind: "search", Status: "start", Query: strings.TrimSpace(args.Query)})

	hits, err := req.Search(ctx, args)
	if err != nil {
		emit(ResearchEvent{Type: "step", Kind: "search", Status: "done", Query: strings.TrimSpace(args.Query)})
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, false
	}

	found := 0
	for _, hit := range hits {
		if hit.ID == "" {
			continue
		}
		state.titles[hit.ID] = hit.Title
		if _, seen := state.seenIDs[hit.ID]; seen {
			continue
		}
		state.seenIDs[hit.ID] = struct{}{}
		state.hits = append(state.hits, hit)
		found++
	}

	emit(ResearchEvent{
		Type:           "step",
		Kind:           "search",
		Status:         "done",
		Query:          strings.TrimSpace(args.Query),
		Count:          len(hits),
		ContextLeftPct: state.budget.LeftPercent(),
	})

	payload, err := json.Marshal(map[string]any{
		"count":              len(hits),
		"documents":          hits,
		"context_chars_left": state.budget.Remaining(),
	})
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"failed to encode search results"}`}, false
	}
	return toolExecResult{ID: callID, Name: name, Content: truncateToolContent(string(payload), state.budget.Remaining())}, found > 0
}

func (a *openAISearchAgent) runReadTool(
	ctx context.Context,
	req ResearchRequest,
	state *researchState,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	ids, err := decodeReadArgs(argumentsJSON)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"invalid tool arguments"}`}, false
	}

	// Only ids the agent has actually seen in this run are readable. Ownership
	// is re-checked by the reader too; this keeps the model from fishing.
	wanted := make([]string, 0, len(ids))
	unknown := make([]string, 0)
	for _, id := range ids {
		if _, ok := state.seenIDs[id]; !ok {
			unknown = append(unknown, id)
			continue
		}
		wanted = append(wanted, id)
	}
	if len(wanted) == 0 {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"no readable ids","hint":"pass ids returned by search_documents in this conversation"}`}, false
	}
	truncatedIDs := false
	if len(wanted) > maxReadIDsPerCall {
		wanted = wanted[:maxReadIDsPerCall]
		truncatedIDs = true
	}
	if repeat, ok := state.claimCall(name, wanted); !ok {
		return toolExecResult{ID: callID, Name: name, Content: repeat}, false
	}

	budget := state.budget.Remaining() - readEnvelopeBaseChars - readEnvelopePerDocChars*len(wanted)
	if budget <= 0 {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"reading budget exhausted","hint":"answer with what you have already read"}`}, false
	}

	emit(ResearchEvent{Type: "step", Kind: "read", Status: "start", Titles: state.titlesFor(wanted), Count: len(wanted)})

	docs, err := req.Read(ctx, wanted, budget)
	if err != nil {
		emit(ResearchEvent{Type: "step", Kind: "read", Status: "done"})
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, false
	}

	newText := 0
	for _, doc := range docs {
		if _, ok := state.read[doc.ID]; ok {
			continue
		}
		state.read[doc.ID] = struct{}{}
		newText += len(doc.Text)
	}

	emit(ResearchEvent{
		Type:           "step",
		Kind:           "read",
		Status:         "done",
		Titles:         state.titlesFor(wanted),
		Count:          len(docs),
		ContextLeftPct: state.budget.LeftPercent(),
	})

	payload := map[string]any{
		"documents":          docs,
		"context_chars_left": state.budget.Remaining(),
	}
	if len(unknown) > 0 {
		payload["skipped_unknown_ids"] = unknown
	}
	if truncatedIDs {
		payload["note"] = fmt.Sprintf("only the first %d ids were read; call read_documents again for the rest", maxReadIDsPerCall)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"failed to encode documents"}`}, false
	}
	return toolExecResult{ID: callID, Name: name, Content: string(encoded)}, newText > 0
}

// claimCall suppresses an identical tool call the run has already made, so a
// model that keeps re-issuing the same query burns a round rather than the
// archive — and stops counting as progress.
func (state *researchState) claimCall(name string, args any) (string, bool) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", true
	}
	key := name + "\x00" + string(encoded)
	if _, ok := state.ran[key]; ok {
		return `{"error":"already ran this exact call","hint":"vary the query or filters, or write the answer"}`, false
	}
	state.ran[key] = struct{}{}
	return "", true
}

func (state *researchState) titlesFor(ids []string) []string {
	titles := make([]string, 0, len(ids))
	for _, id := range ids {
		if title := strings.TrimSpace(state.titles[id]); title != "" {
			titles = append(titles, title)
			continue
		}
		titles = append(titles, id)
	}
	return titles
}

// toolCallChars is what the model's own tool calls cost in the next request.
func toolCallChars(calls []openai.ChatCompletionMessageToolCall) int {
	total := 0
	for _, call := range calls {
		total += len(call.Function.Name) + len(call.Function.Arguments)
	}
	return total
}

func truncateToolContent(content string, remaining int) string {
	limit := maxToolResultBytes
	if remaining > 0 && remaining < limit {
		limit = remaining
	}
	if len(content) <= limit {
		return content
	}
	return strutil.Truncate(content, limit) + strutil.Ellipsis
}

// decodeReadArgs accepts the documented {"ids": [...]} shape and the two forms
// models reach for anyway: a bare string id, and {"id": "..."}.
func decodeReadArgs(data string) ([]string, error) {
	var args readDocumentsArgs
	if err := json.Unmarshal([]byte(data), &args); err == nil && len(args.IDs) > 0 {
		return normalizeIDs(args.IDs), nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}
	ids := coerceStringSlice(raw["ids"])
	if len(ids) == 0 {
		ids = coerceStringSlice(raw["id"])
	}
	if len(ids) == 0 {
		if s := coerceString(raw["document_id"]); s != "" {
			ids = []string{s}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no ids")
	}
	return normalizeIDs(ids), nil
}

func normalizeIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

var citationPattern = regexp.MustCompile(`\[([^\]\n]*)\]\(/document/([A-Za-z0-9_-]+)\)`)

// validateCitations unwraps links to documents the run never saw, so a model
// that invents an id produces plain text rather than a link to nothing.
func validateCitations(reply string, seenIDs map[string]struct{}) string {
	return citationPattern.ReplaceAllStringFunc(reply, func(match string) string {
		parts := citationPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		if _, ok := seenIDs[parts[2]]; ok {
			return match
		}
		return parts[1]
	})
}

const researchAnswerInstruction = `Stop searching and reading. Do not call any tools and do not output tool markup.
Write the final answer for the user now, in markdown, using only what the tool results above actually contain.
Cite each claim with a markdown link to the document it came from: [Document title](/document/<id>), using ids from the tool results.
If you were asked for a total or a comparison, list the per-document figures you extracted before giving the result.
If the evidence is incomplete, say what is missing instead of filling the gap.
Answer in the same language as the user's latest message.`

func buildResearchSystemPrompt(languages, resultLanguage string, availableTags []string) string {
	var b strings.Builder
	b.WriteString(`You are researching the user's personal document archive to answer their question.
Work in steps. First find candidate documents with search_documents, then read the promising ones with read_documents.
Expand the request into concrete keywords and filters. Search bilingual metadata (title/purpose/summary and their *_original fields) plus OCR text.
Prefer precise date_from/date_to, document_type, correspondent, or tags filters when the query implies them.
When filtering by tags, use exact names from the available archive tags list below — never invent tag names.

Never state what a document contains without reading it first. Search results carry a short snippet only; that is a reason to read a document, not evidence about it.
Read in small batches so each document comes back in full rather than truncated.
There is no limit on how many searches or reads you may make. Every tool result reports context_chars_left: when it runs low, stop gathering and write the answer with what you have.
Cite real document ids from tool results only. Never invent a document or an id.
If the archive does not contain the answer, say so plainly and say what is missing.
`)

	b.WriteString(formatAvailableTagsPrompt(availableTags))
	b.WriteString(formatLanguagePrompt(languages, resultLanguage))

	return b.String()
}

func researchTools() []openai.ChatCompletionToolParam {
	tools := searchDocumentsTools()
	return append(tools, openai.ChatCompletionToolParam{
		Function: shared.FunctionDefinitionParam{
			Name:        "read_documents",
			Description: openai.String("Read the full extracted text of documents already returned by search_documents. Use this before making any claim about what a document says."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": fmt.Sprintf("Document ids from earlier search_documents results. At most %d per call; prefer 2-5 so each document is returned in full.", maxReadIDsPerCall),
					},
				},
				"required": []string{"ids"},
			},
		},
	})
}
