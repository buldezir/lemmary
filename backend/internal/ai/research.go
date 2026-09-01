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

// DocumentContent is what read_documents returns to the model: a document's
// text, or as much of it as the budget and the request allowed.
type DocumentContent struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	DocumentDate  string   `json:"document_date,omitempty"`
	DocumentType  string   `json:"document_type,omitempty"`
	Correspondent string   `json:"correspondent,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Text          string   `json:"text"`
	Truncated     bool     `json:"truncated,omitempty"`
	// Excerpted marks text assembled from several parts of the document
	// around a focus rather than read straight through. The gaps are marked
	// in the text itself.
	Excerpted bool `json:"excerpted,omitempty"`
	// PassagesOmitted counts the matching passages that did not fit, so the
	// model can tell "that is all of it" from "there is more like this".
	PassagesOmitted int `json:"passages_omitted,omitempty"`
	// NextOffset is where a sequential read left off, 0 when the document is
	// finished. TotalChars is the whole document's length in the same units,
	// so the model can judge how much is left before spending on it.
	NextOffset int `json:"next_offset,omitempty"`
	TotalChars int `json:"total_chars,omitempty"`
}

// ReadRequest is one read_documents call after validation.
//
// Focus and Offset are the two ways to reach past the head of a long document:
// Focus assembles the parts that match a question, Offset continues straight
// through from where the last read stopped. Without them the tail of anything
// longer than the budget was simply unreachable.
type ReadRequest struct {
	IDs   []string
	Focus string
	// Offset is a byte offset into the document text, as reported by a
	// previous read's NextOffset.
	Offset int
	// MaxTotalChars is the room left in the context window; implementations
	// divide it across the requested ids.
	MaxTotalChars int
}

// DocumentReader loads document text for ids the agent has already seen.
type DocumentReader func(ctx context.Context, req ReadRequest) ([]DocumentContent, error)

type ResearchRequest struct {
	Messages      []ChatMessage
	AvailableTags []string
	Search        DocumentSearcher
	Read          DocumentReader
	// PriorDocuments are the hits earlier turns of this conversation already
	// found. They are readable by id without searching again -- a follow-up
	// question about a document the last answer cited should not have to
	// rediscover it -- but they are not results of this turn, so they only
	// join the answer's document list if the answer cites them.
	PriorDocuments []DocumentHit
}

type ResearchResult struct {
	Reply     string
	Documents []DocumentHit
	// Incomplete marks an answer that was cut off mid-generation and kept
	// anyway. The text is real as far as it goes, but it is not the whole
	// answer, and a caller must not present it as one.
	Incomplete bool
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
	Incomplete     bool          `json:"incomplete,omitempty"`
}

type readDocumentsArgs struct {
	IDs    []string `json:"ids"`
	Focus  string   `json:"focus"`
	Offset int      `json:"offset"`
}

// researchState is everything the loop accumulates across rounds.
type researchState struct {
	budget  *contextBudget
	hits    []DocumentHit
	seenIDs map[string]struct{}
	titles  map[string]string
	read    map[string]struct{}
	// readParts is keyed by document, focus and offset: the same document read
	// with a new question, or continued at next_offset, is progress even
	// though the document itself is not new.
	readParts map[string]struct{}
	ran       map[string]struct{}
	// prior holds documents carried in from earlier turns, by id. They are
	// readable but are not this turn's results until the answer cites one.
	prior map[string]DocumentHit
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
		budget:    newContextBudget(a.contextTokens),
		hits:      make([]DocumentHit, 0),
		seenIDs:   map[string]struct{}{},
		titles:    map[string]string{},
		read:      map[string]struct{}{},
		readParts: map[string]struct{}{},
		ran:       map[string]struct{}{},
		prior:     map[string]DocumentHit{},
	}
	state.seedPrior(req.PriorDocuments)

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
	reply, incomplete, err := a.answerResearch(ctx, apiMessages, emit)
	if err != nil {
		return ResearchResult{}, err
	}

	reply = validateCitations(reply, state.seenIDs)
	state.adoptCitedPrior(reply)
	if strings.TrimSpace(reply) == "" {
		reply = synthesizeSearchReply(state.hits)
		// A synthesized list of hits is a complete answer of its own kind, and
		// nothing of the cut-off text survives into it.
		incomplete = false
	}
	emit(ResearchEvent{Type: "step", Kind: "answer", Status: "done", Count: len(state.read)})

	return ResearchResult{Reply: reply, Documents: state.hits, Incomplete: incomplete}, nil
}

// answerResearch is the second phase: one completion with no tools declared, so
// the model cannot emit tool markup and every chunk is safe to stream.
// It returns the answer and whether it was cut short: the request timeout
// covers the whole generation rather than the gap between chunks, so a long
// answer can fail with most of it already delivered. Keeping that text is right
// — it is better than nothing and the user has already watched it arrive — but
// returning it as an ordinary success is not, because every caller then
// presents a half-finished answer as the finished one.
func (a *openAISearchAgent) answerResearch(
	ctx context.Context,
	apiMessages []openai.ChatCompletionMessageParamUnion,
	emit func(ResearchEvent),
) (reply string, incomplete bool, err error) {
	msgs := append([]openai.ChatCompletionMessageParamUnion{}, apiMessages...)
	msgs = append(msgs, openai.UserMessage(researchAnswerInstruction))

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(a.client.model),
		Messages: msgs,
		// The reserve the whole budget is computed against, made real: without
		// it the answer is free to run past what was held back for it.
		MaxTokens:   openai.Int(answerReserveTokens),
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
			return "", false, ctx.Err()
		}
		if emitted > 0 && strings.TrimSpace(content) != "" {
			// Partial answer already on the wire; keep what arrived rather than
			// replaying a second, different answer over it — but say that it is
			// partial.
			a.client.logger.Warn("research answer stream ended early",
				"chars", len(content),
				slog.Any("error", err),
			)
			return stripDSMLMarkup(strings.TrimSpace(content)), true, nil
		}
		a.client.logger.Warn("research answer stream failed; falling back to a blocking call",
			slog.Any("error", err),
		)
		chatResp, fallbackErr := a.client.complete(ctx, params, "purpose", "research_answer_fallback")
		if fallbackErr != nil {
			return "", false, fmt.Errorf("openai research answer: %w", fallbackErr)
		}
		if len(chatResp.Choices) == 0 {
			return "", false, fmt.Errorf("openai returned no choices")
		}
		content = chatResp.Choices[0].Message.Content
	}

	a.client.logger.Info("research answer complete",
		"chars", len(content),
		"streamed", emitted > 0,
		"duration", time.Since(requestStart).Round(time.Millisecond),
	)
	return stripDSMLMarkup(strings.TrimSpace(content)), false, nil
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

	content, err := encodeSearchResults(hits, state.budget.Remaining())
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"failed to encode search results"}`}, false
	}
	return toolExecResult{ID: callID, Name: name, Content: content}, found > 0
}

// toolSearchHit is a hit as the model sees it. It exists to drop ocr_snippet
// once passages are present: the snippet is the first passage shortened, so
// sending both pays twice for the same sentence out of a budget that is the
// binding constraint on how much of the archive a run can look at.
type toolSearchHit struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	DocumentDate  string    `json:"document_date,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	OCRSnippet    string    `json:"ocr_snippet,omitempty"`
	Passages      []Passage `json:"passages,omitempty"`
	DocumentType  string    `json:"document_type,omitempty"`
	Correspondent string    `json:"correspondent,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
}

// hitTrim is one rung of the fit ladder: what to give up next when the results
// do not fit the budget.
type hitTrim int

const (
	trimNothing hitTrim = iota
	// trimPassages keeps one passage per document. Three passages each is a
	// luxury; one is still evidence.
	trimPassages
	// trimSummaries drops the model-written summaries, which restate metadata
	// the hit already carries, and keeps the verbatim passage.
	trimSummaries
)

func toolSearchHits(hits []DocumentHit, trim hitTrim) []toolSearchHit {
	out := make([]toolSearchHit, 0, len(hits))
	for _, hit := range hits {
		item := toolSearchHit{
			ID:            hit.ID,
			Title:         hit.Title,
			DocumentDate:  hit.DocumentDate,
			Summary:       hit.Summary,
			OCRSnippet:    hit.OCRSnippet,
			Passages:      hit.Passages,
			DocumentType:  hit.DocumentType,
			Correspondent: hit.Correspondent,
			Tags:          hit.Tags,
		}
		if len(item.Passages) > 0 {
			item.OCRSnippet = ""
		}
		if trim >= trimPassages && len(item.Passages) > 1 {
			item.Passages = item.Passages[:1]
		}
		if trim >= trimSummaries {
			item.Summary = ""
		}
		out = append(out, item)
	}
	return out
}

// encodeSearchResults renders hits as a tool result that fits the budget: first
// by giving up detail — the extra passages, then the summaries — and only then
// by dropping whole documents from the tail.
//
// The order is what makes it useful. Losing the third passage of a document
// costs a little evidence; losing the document costs the model any knowledge
// that it exists, and it cannot ask for what it was never shown.
//
// It never slices the encoded JSON: a byte-truncated payload is not JSON at
// all, and near exhaustion — exactly when the model most needs to understand
// its own situation — that was the common case. When not even one document
// fits, the model gets the same structured "budget exhausted" answer the read
// path has always given, so it stops gathering and answers instead.
func encodeSearchResults(hits []DocumentHit, remaining int) (string, error) {
	limit := maxToolResultBytes
	if remaining < limit {
		limit = remaining
	}
	if limit <= 0 {
		return budgetExhaustedResult, nil
	}

	encode := func(count int, trim hitTrim) (string, bool, error) {
		payload := map[string]any{
			"count":              count,
			"documents":          toolSearchHits(hits[:count], trim),
			"context_chars_left": remaining,
		}
		if count < len(hits) {
			payload["note"] = fmt.Sprintf(
				"only the first %d of %d results fit the remaining context", count, len(hits))
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", false, err
		}
		return string(encoded), len(encoded) <= limit, nil
	}

	for _, trim := range []hitTrim{trimNothing, trimPassages, trimSummaries} {
		content, fits, err := encode(len(hits), trim)
		if err != nil {
			return "", err
		}
		if fits {
			return content, nil
		}
	}
	for count := len(hits) - 1; count > 0; count-- {
		content, fits, err := encode(count, trimSummaries)
		if err != nil {
			return "", err
		}
		if fits {
			return content, nil
		}
	}
	return budgetExhaustedResult, nil
}

const budgetExhaustedResult = `{"error":"context budget exhausted","hint":"answer with what you have already found"}`

func (a *openAISearchAgent) runReadTool(
	ctx context.Context,
	req ResearchRequest,
	state *researchState,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	args, err := decodeReadArgs(argumentsJSON)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"invalid tool arguments"}`}, false
	}
	focus := strings.TrimSpace(args.Focus)
	offset := args.Offset
	if offset < 0 {
		offset = 0
	}

	// Only ids the agent has seen -- in this run or in an earlier turn of the
	// same conversation -- are readable. Ownership is re-checked by the reader
	// too; this keeps the model from fishing.
	wanted := make([]string, 0, len(args.IDs))
	unknown := make([]string, 0)
	for _, id := range args.IDs {
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
	// Focus and offset are part of the call's identity: re-reading the same
	// document with a different question, or continuing it at next_offset, is
	// new work rather than a repeat.
	claim := readClaim{IDs: wanted, Focus: focus, Offset: offset}
	if repeat, ok := state.claimCall(name, claim); !ok {
		return toolExecResult{ID: callID, Name: name, Content: repeat}, false
	}

	budget := state.budget.Remaining() - readEnvelopeBaseChars - readEnvelopePerDocChars*len(wanted)
	if budget <= 0 {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"reading budget exhausted","hint":"answer with what you have already read"}`}, false
	}

	emit(ResearchEvent{Type: "step", Kind: "read", Status: "start", Titles: state.titlesFor(wanted), Count: len(wanted)})

	docs, err := req.Read(ctx, ReadRequest{
		IDs:           wanted,
		Focus:         focus,
		Offset:        offset,
		MaxTotalChars: budget,
	})
	if err != nil {
		emit(ResearchEvent{Type: "step", Kind: "read", Status: "done"})
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, false
	}

	newText := 0
	for _, doc := range docs {
		state.read[doc.ID] = struct{}{}
		key := fmt.Sprintf("%s\x00%s\x00%d", doc.ID, focus, offset)
		if _, ok := state.readParts[key]; ok {
			continue
		}
		state.readParts[key] = struct{}{}
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

// readClaim identifies one read: the same ids asked a different question, or
// continued from a different offset, are different reads.
type readClaim struct {
	IDs    []string `json:"ids"`
	Focus  string   `json:"focus,omitempty"`
	Offset int      `json:"offset,omitempty"`
}

// seedPrior makes the documents of earlier turns readable without searching
// again. They go into seenIDs and titles but not into hits: this turn has not
// found them, and listing them as its results would attach documents to an
// answer that never mentions them.
//
// Passages are dropped on the way in. They were selected for the question that
// turn asked, and quoting them under a different one is misleading; if the
// document matters here, the model reads it.
func (state *researchState) seedPrior(docs []DocumentHit) {
	for _, doc := range docs {
		if doc.ID == "" {
			continue
		}
		if _, ok := state.prior[doc.ID]; ok {
			continue
		}
		doc.Passages = nil
		state.prior[doc.ID] = doc
		state.seenIDs[doc.ID] = struct{}{}
		if title := strings.TrimSpace(doc.Title); title != "" {
			state.titles[doc.ID] = title
		}
	}
}

// adoptCitedPrior promotes an earlier turn's document into this turn's results
// once the answer has cited it, so the citation resolves to a card the user can
// click. Called after validateCitations, which has already removed links to ids
// the run never saw.
func (state *researchState) adoptCitedPrior(reply string) {
	if len(state.prior) == 0 {
		return
	}
	inHits := make(map[string]struct{}, len(state.hits))
	for _, hit := range state.hits {
		inHits[hit.ID] = struct{}{}
	}
	for _, match := range citationPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) != 3 {
			continue
		}
		doc, ok := state.prior[match[2]]
		if !ok {
			continue
		}
		if _, dup := inHits[doc.ID]; dup {
			continue
		}
		inHits[doc.ID] = struct{}{}
		state.hits = append(state.hits, doc)
	}
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

// decodeReadArgs accepts the documented {"ids": [...]} shape and the two forms
// models reach for anyway: a bare string id, and {"id": "..."}. focus and
// offset are coerced the same way search arguments are, because a model that
// sends "offset": "1500" means the number.
func decodeReadArgs(data string) (readDocumentsArgs, error) {
	var args readDocumentsArgs
	if err := json.Unmarshal([]byte(data), &args); err == nil && len(args.IDs) > 0 {
		args.IDs = normalizeIDs(args.IDs)
		return args, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return readDocumentsArgs{}, err
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
		return readDocumentsArgs{}, fmt.Errorf("no ids")
	}
	return readDocumentsArgs{
		IDs:    normalizeIDs(ids),
		Focus:  coerceString(raw["focus"]),
		Offset: coerceInt(raw["offset"]),
	}, nil
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

// citationPattern matches the document links an answer is written with. The
// optional ?page=N is tolerated rather than required: nothing asks the model
// for page numbers yet, but a model that adds one must not have its citation
// silently unwrapped as if the id were invented.
var citationPattern = regexp.MustCompile(`\[([^\]\n]*)\]\(/document/([A-Za-z0-9_-]+)(?:\?page=\d+)?\)`)

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

Never state what a document contains without reading it first. Search results carry a few verbatim passages; a passage is a reason to read the document, not the whole of what it says.
Read in small batches so each document comes back in full rather than truncated.
For a long document, pass focus with the question you are trying to answer: the read then returns the parts about it instead of only the beginning, with gaps marked by …
When a read reports next_offset, the document continues there: call read_documents again with that offset to carry on where you stopped.
Documents cited earlier in this conversation can be read by id straight away; you do not have to search for them again.
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
			Name: "read_documents",
			Description: openai.String("Read the extracted text of documents already seen in this conversation. " +
				"Use this before making any claim about what a document says."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": fmt.Sprintf("Document ids from earlier search_documents results, or cited earlier in this conversation. At most %d per call; prefer 2-5 so each document is returned in full.", maxReadIDsPerCall),
					},
					"focus": map[string]any{
						"type": "string",
						"description": "What you are looking for in these documents. " +
							"For a document too long to return whole, the passages about this are returned instead of only the beginning, with … marking the gaps.",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Continue a straight read from this position, as reported by a previous result's next_offset. Omit to start at the beginning.",
					},
				},
				"required": []string{"ids"},
			},
		},
	})
}
