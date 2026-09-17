package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/logfmt"
	"lemmary/backend/internal/websearch"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

const (
	// maxStalledRounds ends the research phase when the model keeps calling
	// tools that surface nothing new. This is a stall detector, not a round
	// cap: a run that keeps finding documents is never cut short.
	maxStalledRounds = 3
)

// DocumentContent is what read_documents returns to the model: a document's
// text, or — when the call named a focus — the parts of it about that focus.
type DocumentContent struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	DocumentDate  string   `json:"document_date,omitempty"`
	DocumentType  string   `json:"document_type,omitempty"`
	Correspondent string   `json:"correspondent,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Text          string   `json:"text,omitempty"`
	// Excerpted marks text assembled from several parts of the document
	// around a focus rather than read straight through. The gaps are marked
	// in the text itself.
	Excerpted bool `json:"excerpted,omitempty"`
	// FocusUsed names the question the excerpt was chosen by when the call
	// gave no focus of its own, so the model knows what the passages answer.
	FocusUsed string `json:"focus_used,omitempty"`
	// PassagesOmitted counts the matching passages that did not fit, so the
	// model can tell "that is all of it" from "there is more like this".
	PassagesOmitted int `json:"passages_omitted,omitempty"`

	// Distilled marks a document the helper model read on the agent's
	// behalf: Notes, Quotes and Values stand in for Text, which is absent.
	// The agent never sees the document itself, only what it says about the
	// question.
	Distilled bool `json:"distilled,omitempty"`
	// Relevant is the helper's judgement of whether the document bears on
	// the question at all. Meaningful only when Distilled.
	Relevant bool              `json:"relevant,omitempty"`
	Notes    string            `json:"notes,omitempty"`
	Quotes   []string          `json:"quotes,omitempty"`
	Values   map[string]string `json:"values,omitempty"`
}

// ReadRequest is one read_documents call after validation. Focus is retrieval,
// not rationing: a document read whole answers with its first pages, and on a
// fifty-page statement the paragraph that matters is rarely there.
type ReadRequest struct {
	IDs   []string
	Focus string
	// Question is the user's latest message. A long document read with no
	// focus is excerpted around it, because "the beginning" is rarely where
	// the answer is, and it is what a distilled read is distilled toward.
	Question string
}

// DocumentReader loads document text for ids the agent has already seen.
type DocumentReader func(ctx context.Context, req ReadRequest) ([]DocumentContent, error)

type ResearchRequest struct {
	// Thread is the conversation as it was last sent to the provider, read back
	// from storage: the system prompt, every question, every tool call and its
	// result, every answer. It is replayed verbatim, which is what lets a
	// follow-up build on earlier turns and what gives the provider a prefix it
	// has already cached. The caller appends the new question to it before the
	// run; a Thread that ends mid-turn resumes that turn instead.
	Thread []ThreadMessage
	// Record is handed every message this run adds, in order, as it happens, so
	// a run that dies leaves its work behind. The final answer is not recorded
	// here: it comes back in ResearchResult, and the caller stores it with the
	// documents and usage that belong to it.
	Record func(ThreadMessage)
	// AvailableTags is read only when the conversation has no system prompt
	// yet; after that the stored one is what the model sees.
	AvailableTags []string
	Search        DocumentSearcher
	Read          DocumentReader
	// PriorDocuments are the hits earlier turns of this conversation already
	// found. Readable by id without searching again, but not results of this
	// turn, so they join the answer's document list only if the answer cites them.
	PriorDocuments []DocumentHit
	// DenseRetrieval says searches match by meaning as well as by keyword;
	// see SearchOptions.
	DenseRetrieval bool
	// Survey and Count back survey_documents and count_documents. Either may be
	// nil -- a helper model can be bound and unbound between two questions of
	// one conversation -- and the tool is declared anyway, because the list is
	// part of what the provider cached. A call with nothing behind it is
	// refused.
	Survey DocumentSurveyor
	Count  DocumentCounter
	// Web backs web_search and web_fetch. Nil unless an operator configured a
	// web-search provider and the user asked for it on this turn. The tools are
	// declared either way -- the list is part of what the provider cached, and
	// this is per turn -- and a call made without them behind it is refused.
	Web *websearch.Tavily
	// ContextWindow is the bound model's context length in tokens, or 0 when
	// nobody knows it. Reported, never enforced: what fits is the provider's
	// ruling, and it delivers it by refusing the request.
	ContextWindow int
}

type ResearchResult struct {
	Reply     string
	Documents []DocumentHit
	// Incomplete marks an answer that was cut off mid-generation and kept
	// anyway. The text is real as far as it goes, but it is not the whole
	// answer, and a caller must not present it as one.
	Incomplete bool
	// Usage is how much context the run's own conversation took at its widest.
	Usage TurnUsage
}

// ResearchEvent is one line of the run's visible progress. Types: "step",
// "delta", "documents", "message", "usage", "error", "done".
type ResearchEvent struct {
	Type   string   `json:"type"`
	Kind   string   `json:"kind,omitempty"`   // search | read | survey | count | answer
	Status string   `json:"status,omitempty"` // start | progress | done
	Query  string   `json:"query,omitempty"`
	Titles []string `json:"titles,omitempty"`
	Count  int      `json:"count,omitempty"`
	// Done is the running count of a step with progress: documents surveyed
	// so far, out of Count.
	Done int `json:"done,omitempty"`
	// Distilled marks a read step whose documents the helper model read and
	// summarised rather than being passed through whole.
	Distilled  bool          `json:"distilled,omitempty"`
	Content    string        `json:"content,omitempty"`
	Documents  []DocumentHit `json:"documents,omitempty"`
	Message    string        `json:"message,omitempty"`
	Incomplete bool          `json:"incomplete,omitempty"`
	// PromptTokens, ContextWindow and Estimated carry a "usage" event: how
	// wide the conversation has grown, out of what, and whether the number was
	// counted by the provider or estimated from the text we sent.
	PromptTokens  int  `json:"prompt_tokens,omitempty"`
	ContextWindow int  `json:"context_window,omitempty"`
	Estimated     bool `json:"estimated,omitempty"`
}

type readDocumentsArgs struct {
	IDs   []string `json:"ids"`
	Focus string   `json:"focus"`
}

// researchState is everything the loop accumulates across rounds.
type researchState struct {
	hits    []DocumentHit
	seenIDs map[string]struct{}
	titles  map[string]string
	read    map[string]struct{}
	// readParts is keyed by document and focus: the same document read with a
	// new question is progress even though the document itself is not new.
	readParts map[string]struct{}
	ran       map[string]struct{}
	// prior holds documents carried in from earlier turns, by id. They are
	// readable but are not this turn's results until the answer cites one.
	prior map[string]DocumentHit
	// question is the user's latest message, handed to every read so a
	// document read without a focus is still read for something.
	question string
	// web is this run's remaining web-call allowance; see maxWebCalls.
	web webBudget
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
	record := req.Record
	if record == nil {
		record = func(ThreadMessage) {}
	}
	ctx = aiprovider.EnsureSession(ctx, "research")

	state := &researchState{
		hits:      make([]DocumentHit, 0),
		seenIDs:   map[string]struct{}{},
		titles:    map[string]string{},
		read:      map[string]struct{}{},
		readParts: map[string]struct{}{},
		ran:       map[string]struct{}{},
		prior:     map[string]DocumentHit{},
	}
	state.seedPrior(req.PriorDocuments)

	thread := req.Thread
	// A conversation keeps the prompt it was opened with, the way it already
	// keeps its model: rebuilding it per turn from a live tag query would move
	// the prefix under the provider's cache on every question. A caller that
	// stores the thread writes this row itself, before the question, so the
	// stored order is the order it replays in; prepending it here is for the
	// callers that store nothing.
	if !startsWithSystem(thread) {
		thread = append([]ThreadMessage{{Role: "system", Content: a.SystemPrompt(req)}}, thread...)
	}
	state.question = latestUserMessage(thread)
	if state.question == "" {
		return ResearchResult{}, fmt.Errorf("at least one user message is required")
	}

	meter := newContextMeter(req.ContextWindow)
	apiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(thread))
	for _, msg := range thread {
		param, ok := msg.Param()
		if !ok {
			continue
		}
		meter.grew(msg.Size())
		apiMessages = append(apiMessages, param)
	}

	// Every schema, every turn, whatever is behind them. The tool list is part
	// of what the provider cached, and what backs these three moves underneath
	// a conversation: the web toggle is per question, and a helper or a counter
	// can be bound or unbound between two of them. A list that followed would
	// forfeit the whole transcript's prefix each time. A call with nothing
	// behind it is refused instead -- see runWebTool, runSurveyTool and
	// runCountTool -- which costs one round and no cache.
	tools := append(researchTools(),
		surveyDocumentsTool(),
		countDocumentsTool(),
		webSearchTool(),
		webFetchTool(),
	)
	stalled := 0
	round := 0
	var usage Usage

	// No round cap: the loop ends when the model is ready, when it stops making
	// progress, or when a completion is rejected. Every iteration appends at
	// least an assistant message and a tool result, so a run that keeps
	// gathering is finite: the provider will refuse the next request.
	for {
		if err := ctx.Err(); err != nil {
			return ResearchResult{}, err
		}
		if stalled >= maxStalledRounds {
			a.client.logger.Info("research stalled; answering", "round", round, "documents", len(state.hits))
			break
		}

		requestStart := time.Now()
		chatResp, err := a.client.Complete(ctx, openai.ChatCompletionNewParams{
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
		)
		if err != nil {
			a.client.logger.Error("research request failed",
				"round", round,
				logfmt.Duration("duration", time.Since(requestStart)),
				slog.Any("error", err),
			)
			return ResearchResult{}, fmt.Errorf("openai research completion: %w", err)
		}
		if len(chatResp.Choices) == 0 {
			return ResearchResult{}, fmt.Errorf("openai returned no choices")
		}
		usage.Add(usageOf(chatResp))
		emit(usageEvent(meter.observe(usageOf(chatResp))))

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
			asked := ThreadMessage{Role: "assistant", Content: msg.Content}
			for _, call := range nativeCalls {
				asked.Calls = append(asked.Calls, ToolCall{
					ID:        call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				})
			}
			// Recorded before the tools run, so a run that dies inside one still
			// shows what it was doing.
			record(asked)
			apiMessages = append(apiMessages, msg.ToParam())
			meter.grew(asked.Size())

			for _, call := range nativeCalls {
				result, advanced := a.runResearchTool(ctx, req, state, call.ID, call.Function.Name, call.Function.Arguments, emit)
				progressed = progressed || advanced
				answer := ThreadMessage{Role: "tool", Content: result.Content, CallID: call.ID}
				record(answer)
				meter.grew(answer.Size())
				apiMessages = append(apiMessages, openai.ToolMessage(result.Content, call.ID))
			}
		} else {
			// DSML models put tool calls in content; feed results back as a user message.
			said := ThreadMessage{Role: "assistant", Content: msg.Content}
			record(said)
			apiMessages = append(apiMessages, openai.AssistantMessage(msg.Content))
			meter.grew(said.Size())

			results := make([]toolExecResult, 0, len(dsmlCalls))
			for _, call := range dsmlCalls {
				result, advanced := a.runResearchTool(ctx, req, state, call.ID, call.Name, call.Arguments, emit)
				progressed = progressed || advanced
				results = append(results, result)
			}
			formatted := formatDSMLToolResults(results)
			// Stored as a tool row with no call id; ThreadMessage.Param turns
			// it back into the user message this dialect expects.
			fed := ThreadMessage{Role: "tool", Content: formatted}
			record(fed)
			meter.grew(fed.Size())
			apiMessages = append(apiMessages, openai.UserMessage(formatted))
		}

		if progressed {
			stalled = 0
		} else {
			stalled++
		}
		round++
	}

	emit(ResearchEvent{Type: "step", Kind: "answer", Status: "start"})
	reply, incomplete, answerUsage, err := a.answerResearch(ctx, apiMessages, tools, req.Web != nil, meter, emit)
	if err != nil {
		return ResearchResult{}, err
	}
	usage.Add(answerUsage)
	a.client.logger.Info("research run usage",
		"rounds", round,
		"documents", len(state.hits),
		"read", len(state.read),
		"prompt_tokens", usage.Prompt,
		"cached_tokens", usage.Cached,
		"completion_tokens", usage.Completion,
	)

	reply = validateCitations(reply, state.seenIDs)
	state.adoptCitedPrior(reply)
	if strings.TrimSpace(reply) == "" {
		reply = synthesizeSearchReply(state.hits)
		// A synthesized list of hits is a complete answer of its own kind, and
		// nothing of the cut-off text survives into it.
		incomplete = false
	}
	emit(ResearchEvent{Type: "step", Kind: "answer", Status: "done", Count: len(state.read)})

	return ResearchResult{Reply: reply, Documents: state.hits, Incomplete: incomplete, Usage: meter.usage}, nil
}

// answerResearch is the second phase: one completion with the tools refused
// rather than removed, so the model cannot emit tool markup and every chunk is
// safe to stream, while the prefix the rounds before it cached still holds. It
// returns the answer and whether it was cut short: the request timeout covers
// the whole generation, so a long answer can fail with most of it delivered.
// That text is worth keeping, but not as an ordinary success, or every caller
// presents a half-finished answer as the finished one.
func (a *openAISearchAgent) answerResearch(
	ctx context.Context,
	apiMessages []openai.ChatCompletionMessageParamUnion,
	tools []openai.ChatCompletionToolUnionParam,
	web bool,
	meter *contextMeter,
	emit func(ResearchEvent),
) (reply string, incomplete bool, usage Usage, err error) {
	instruction := researchAnswerInstruction(web)
	meter.grew(len(instruction))
	// Deferred so every exit reports, the partial answer and the blocking
	// fallback included: the prompt was sent whatever became of the reply.
	defer func() { emit(usageEvent(meter.observe(usage))) }()

	msgs := append([]openai.ChatCompletionMessageParamUnion{}, apiMessages...)
	msgs = append(msgs, openai.UserMessage(instruction))

	// The same tools as the rounds before, refused rather than removed. A
	// provider caches the tool list as part of the prefix, so dropping it here
	// would re-bill the whole conversation on the largest request of the turn;
	// tool_choice "none" buys the text answer without moving the prefix.
	params := openai.ChatCompletionNewParams{
		Model:       shared.ChatModel(a.client.model),
		Messages:    msgs,
		Temperature: CompletionTemperature(a.client.model, 0.2),
		Tools:       tools,
	}
	if len(tools) > 0 {
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.String("none"),
		}
	}

	emitted := 0
	requestStart := time.Now()
	content, usage, err := a.client.completeStreaming(ctx, params, func(delta string) {
		emitted++
		emit(ResearchEvent{Type: "delta", Content: delta})
	}, "purpose", "research_answer", "messages", len(msgs))

	if err != nil {
		if ctx.Err() != nil {
			return "", false, usage, ctx.Err()
		}
		if emitted > 0 && strings.TrimSpace(content) != "" {
			// Partial answer already on the wire; keep what arrived rather than
			// replaying a second, different answer over it — but say that it is
			// partial.
			a.client.logger.Warn("research answer stream ended early",
				"chars", len(content),
				slog.Any("error", err),
			)
			return stripDSMLMarkup(strings.TrimSpace(content)), true, usage, nil
		}
		a.client.logger.Warn("research answer stream failed; falling back to a blocking call",
			slog.Any("error", err),
		)
		chatResp, fallbackErr := a.client.Complete(ctx, params, "purpose", "research_answer_fallback")
		if fallbackErr != nil {
			return "", false, usage, fmt.Errorf("openai research answer: %w", fallbackErr)
		}
		if len(chatResp.Choices) == 0 {
			return "", false, usage, fmt.Errorf("openai returned no choices")
		}
		usage = usageOf(chatResp)
		content = chatResp.Choices[0].Message.Content
	}

	a.client.logger.Info("research answer complete",
		"chars", len(content),
		"streamed", emitted > 0,
		logfmt.Duration("duration", time.Since(requestStart)),
	)
	return stripDSMLMarkup(strings.TrimSpace(content)), false, usage, nil
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
	case "survey_documents":
		return a.runSurveyTool(ctx, req, state, callID, name, argumentsJSON, emit)
	case "count_documents":
		return a.runCountTool(ctx, req, state, callID, name, argumentsJSON, emit)
	case "web_search", "web_fetch":
		return runWebTool(ctx, req.Web, &state.web, callID, name, argumentsJSON, emit)
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
		Type:   "step",
		Kind:   "search",
		Status: "done",
		Query:  strings.TrimSpace(args.Query),
		Count:  len(hits),
	})

	content, err := encodeSearchResults(hits)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"failed to encode search results"}`}, false
	}
	return toolExecResult{ID: callID, Name: name, Content: content}, found > 0
}

// toolSearchHit is a hit as the model sees it. It exists to drop ocr_snippet
// once passages are present: the snippet is the first passage shortened, so
// sending both spends the conversation twice on the same sentence.
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

func toolSearchHits(hits []DocumentHit) []toolSearchHit {
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
		out = append(out, item)
	}
	return out
}

// encodeSearchResults renders the whole hit list. Nothing is dropped and
// nothing is sliced: the only limit on how much a run may gather is the one the
// provider enforces, and a payload trimmed to a guessed window cost the model
// documents it could then never ask about.
func encodeSearchResults(hits []DocumentHit) (string, error) {
	encoded, err := json.Marshal(map[string]any{
		"count":     len(hits),
		"documents": toolSearchHits(hits),
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

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
	// Focus is part of the call's identity: re-reading the same document with
	// a different question is new work rather than a repeat.
	claim := readClaim{IDs: wanted, Focus: focus}
	if repeat, ok := state.claimCall(name, claim); !ok {
		return toolExecResult{ID: callID, Name: name, Content: repeat}, false
	}

	emit(ResearchEvent{Type: "step", Kind: "read", Status: "start", Titles: state.titlesFor(wanted), Count: len(wanted)})

	docs, err := req.Read(ctx, ReadRequest{IDs: wanted, Focus: focus, Question: state.question})
	if err != nil {
		emit(ResearchEvent{Type: "step", Kind: "read", Status: "done"})
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, false
	}

	newText := 0
	distilled := false
	for _, doc := range docs {
		state.read[doc.ID] = struct{}{}
		distilled = distilled || doc.Distilled
		key := doc.ID + "\x00" + focus
		if _, ok := state.readParts[key]; ok {
			continue
		}
		state.readParts[key] = struct{}{}
		newText += len(doc.Text) + len(doc.Notes)
	}

	emit(ResearchEvent{
		Type:      "step",
		Kind:      "read",
		Status:    "done",
		Titles:    state.titlesFor(wanted),
		Count:     len(docs),
		Distilled: distilled,
	})

	payload := map[string]any{
		"documents": docs,
	}
	if len(unknown) > 0 {
		payload["skipped_unknown_ids"] = unknown
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"failed to encode documents"}`}, false
	}
	return toolExecResult{ID: callID, Name: name, Content: string(encoded)}, newText > 0
}

// readClaim identifies one read: the same ids asked a different question are
// different reads.
type readClaim struct {
	IDs   []string `json:"ids"`
	Focus string   `json:"focus,omitempty"`
}

// seedPrior makes the documents of earlier turns readable without searching
// again. They go into seenIDs and titles but not into hits: this turn has not
// found them. Passages are dropped on the way in, having been selected for the
// question that turn asked.
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
// click. Called after validateCitations.
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

// decodeReadArgs accepts the documented {"ids": [...]} shape and the two forms
// models reach for anyway: a bare string id, and {"id": "..."}. focus is
// coerced the same way search arguments are, because models get JSON scalar
// types wrong.
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
		IDs:   normalizeIDs(ids),
		Focus: coerceString(raw["focus"]),
	}, nil
}

// latestUserMessage is the question the run is answering: the last user turn.
// Tool results are stored as tool rows even when the dialect feeds them back
// as user messages,
// so the loop talking to itself cannot be mistaken for the question -- which
// would send every read off to focus on a JSON blob.
func latestUserMessage(thread []ThreadMessage) string {
	for i := len(thread) - 1; i >= 0; i-- {
		if thread[i].Role != "user" {
			continue
		}
		if content := strings.TrimSpace(thread[i].Content); content != "" {
			return content
		}
	}
	return ""
}

func startsWithSystem(thread []ThreadMessage) bool {
	return len(thread) > 0 && thread[0].Role == "system"
}

// SystemPrompt is the instruction a conversation opens with, for the caller
// that stores it. Built from what the archive looks like now, so it is asked
// for once per conversation and replayed on every turn after.
func (a *openAISearchAgent) SystemPrompt(req ResearchRequest) string {
	return buildResearchSystemPrompt(a.languages, a.resultLanguage, req.AvailableTags, req.DenseRetrieval)
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

func researchAnswerInstruction(web bool) string {
	instruction := `Stop searching and reading. Do not call any tools and do not output tool markup.
Write the final answer for the user now, in markdown, using only what the tool results above actually contain.
Cite each claim with a markdown link to the document it came from: [Document title](/document/<id>), using ids from the tool results.
If you were asked for a total or a comparison, list the per-document figures you extracted before giving the result; when a survey reported totals, use those figures rather than adding rows yourself.
If the evidence is incomplete, say what is missing instead of filling the gap.
Answer in the same language as the user's latest message.`
	if web {
		instruction += `
Cite a claim taken from the web as [Page title](https://...), with the URL the tool returned. Say which claims came from the web rather than from the archive.`
	}
	return instruction
}

func buildResearchSystemPrompt(languages, resultLanguage string, availableTags []string, dense bool) string {
	var b strings.Builder
	b.WriteString(`You are researching the user's personal document archive to answer their question.
Work in steps. First find candidate documents with search_documents, then read the promising ones with read_documents.
Only documents that have finished processing are searched, surveyed and counted; one still pending, failed or awaiting review cannot be found.
Expand the request into concrete keywords and filters. Search bilingual metadata (title/purpose/summary and their *_original fields) plus OCR text.
Prefer precise date_from/date_to, document_type, correspondent, or tags filters when the query implies them.
When filtering by tags, use exact names from the available archive tags list below — never invent tag names.

Never state what a document contains without reading it first. Search results carry a few verbatim passages; a passage is a reason to read the document, not the whole of what it says.
A long document is returned as an excerpt around a focus, with gaps marked by …; without a focus of your own the user's question is used. Pass focus to steer the excerpt toward what you need.
A read of many documents, or of a lot of text, comes back distilled: for each document, notes on what it says about the focus and verbatim quotes, instead of the text itself. Cite distilled documents as you would any other.
Documents cited earlier in this conversation can be read by id straight away; you do not have to search for them again.
For a question about many documents at once -- a topic, everything from one correspondent, a total over a year -- use survey_documents once with the question and the fields you need instead of reading documents one by one. Its rows and totals are evidence you may cite.
For how-many or distribution questions call count_documents with the filters instead of counting search results: a search result is a capped page, not the archive.
There is no limit on how many searches or reads you may make. Stop gathering and write the answer once you have enough evidence.
Not every tool is backed on every question. When a call comes back saying it is not available or not enabled, do not try it again: work with the tools that answer, and say what you could not check.
Cite real document ids from tool results only. Never invent a document or an id.
If the archive does not contain the answer, say so plainly and say what is missing.

You can also reach the public web with web_search and web_fetch, for what the archive cannot hold: current prices, rates and rules, a company's present details, anything that changed after the documents were written. Web access is granted per question, so these are the calls most likely to come back refused.
The archive is still the primary source. Search it first, and use the web to check or complete what you found there rather than instead of looking.
A search result's snippet is a reason to fetch the page, not the whole of what it says: web_fetch before claiming what a page contains, exactly as you would read a document.
Web calls are limited and billed; make them count.
`)

	b.WriteString(formatAvailableTagsPrompt(availableTags))
	b.WriteString(formatLanguagePrompt(languages, resultLanguage, dense))

	return b.String()
}

func researchTools() []openai.ChatCompletionToolUnionParam {
	tools := searchDocumentsTools()
	return append(tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name: "read_documents",
		Description: openai.String("Read documents already seen in this conversation. " +
			"Use this before making any claim about what a document says. " +
			"Long documents come back as excerpts around the focus (or the user's question); " +
			"reading many documents at once comes back as per-document notes and quotes rather than text."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Document ids from earlier search_documents results, or cited earlier in this conversation.",
				},
				"focus": map[string]any{
					"type": "string",
					"description": "What you are looking for in these documents. " +
						"For a long document the passages about this are returned instead of only the beginning, with … marking the gaps. " +
						"Defaults to the user's question.",
				},
			},
			"required": []string{"ids"},
		},
	}))
}
