package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/logfmt"
	"lemmary/backend/internal/models"
)

// Helper is the model Deep Search hands bulk per-document work to. The research
// loop is a few completions over a growing conversation; the helper is many
// completions over one document each, and none of what it reads enters that
// conversation.
type Helper interface {
	Name() string
	Model() string
	// Distill answers one question against each of the given documents and
	// extracts the requested fields, using nothing but the documents' text.
	Distill(ctx context.Context, req DistillRequest) (DistillResult, error)
	// Screen judges from metadata and matched passages alone whether each
	// document could bear on the question. It is the cheap first pass over
	// every candidate; Distill is the read that follows for the survivors.
	Screen(ctx context.Context, req ScreenRequest) (ScreenResult, error)
}

// ScreenDoc is one candidate as the screen sees it: everything the archive
// knows about the document short of its text, plus the passages the search
// matched.
type ScreenDoc struct {
	ID            string
	Title         string
	TitleOriginal string
	DocumentDate  string
	DocumentType  string
	Correspondent string
	Tags          []string
	People        []string
	Purpose       string
	Summary       string
	PageCount     int
	Passages      []string
}

type ScreenRequest struct {
	Question string
	Docs     []ScreenDoc
}

// Screen verdicts. A document the helper did not mention is treated as
// VerdictMaybe by the caller: silence never drops a candidate.
const (
	VerdictYes   = "yes"
	VerdictMaybe = "maybe"
	VerdictNo    = "no"
)

type ScreenRow struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
}

type ScreenResult struct {
	Rows  []ScreenRow
	Usage Usage
}

// SurveyField is one value the caller wants pulled out of every document.
type SurveyField struct {
	Name string `json:"name"`
	// Type is "string", "number" or "date". Numbers come back with a dot
	// decimal separator and no unit, so the caller can add them up; a
	// currency, when there is one, comes back as "<name>_currency".
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// DistillDoc is one document as the helper sees it: the metadata that gives
// the text a frame, and the text itself, whole or excerpted.
type DistillDoc struct {
	ID            string
	Title         string
	DocumentDate  string
	DocumentType  string
	Correspondent string
	Text          string
	// Excerpted marks text with gaps: the helper is told so it does not
	// report as absent what was simply not shown.
	Excerpted bool
}

type DistillRequest struct {
	// Question is what the notes and quotes are about. Required: a distil
	// without a question is a summary.
	Question string
	Fields   []SurveyField
	Docs     []DistillDoc
}

// DistillRow is the helper's answer for one document.
type DistillRow struct {
	ID string `json:"id"`
	// Relevant is the helper's judgement that the document bears on the
	// question at all. Irrelevant rows carry no notes.
	Relevant bool `json:"relevant"`
	// Notes are what the document says about the question, in the helper's
	// words, a few sentences at most.
	Notes string `json:"notes,omitempty"`
	// Quotes are verbatim passages behind the notes, for the answer to cite.
	Quotes []string `json:"quotes,omitempty"`
	// Values are the requested fields, by name, as strings. Absent fields
	// are listed in Missing rather than given as empty strings.
	Values  map[string]string `json:"values,omitempty"`
	Missing []string          `json:"missing,omitempty"`
	// Chunks are the ordinals of the [chunk N] markers whose text backs the
	// notes, when the text carried markers. They are what read_chunks takes.
	Chunks []int `json:"chunks,omitempty"`
}

type DistillResult struct {
	Rows  []DistillRow
	Usage Usage
}

// maxHelperTimeout caps how long one distill call may take, however generous
// the shared AI timeout is. The helper is the fast leg of a research run and a
// batch that does not come back is passed through as raw text, so failing fast
// reaches that fallback sooner. Left uncapped, a helper that never answered
// burned the full AI timeout per batch, several batches to a run.
const maxHelperTimeout = 45 * time.Second

// helperTimeout clamps the shared AI timeout down to what a helper call is
// worth waiting for.
func helperTimeout(shared time.Duration) time.Duration {
	if shared <= 0 || shared > maxHelperTimeout {
		return maxHelperTimeout
	}
	return shared
}

// NewHelper builds a Helper on an OpenAI-compatible chat endpoint.
func NewHelper(sdk, apiKey, model, baseURL string, timeout time.Duration, logger *slog.Logger, extra ...option.RequestOption) Helper {
	return &openAIHelper{client: NewOpenAIClient(sdk, apiKey, model, baseURL, "", "", helperTimeout(timeout), logger, extra...)}
}

type openAIHelper struct {
	client *OpenAIClient
}

func (h *openAIHelper) Name() string  { return h.client.Name() }
func (h *openAIHelper) Model() string { return h.client.Model() }

func (h *openAIHelper) Distill(ctx context.Context, req DistillRequest) (DistillResult, error) {
	if h.client.apiKey == "" {
		return DistillResult{}, fmt.Errorf("AI API key is not configured")
	}
	if len(req.Docs) == 0 {
		return DistillResult{}, nil
	}
	// Normally the surrounding research turn's conversation, inherited from the
	// context the tool call was made under.
	ctx = aiprovider.EnsureSession(ctx, "distill")
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return DistillResult{}, fmt.Errorf("distill needs a question")
	}

	requestStart := time.Now()
	resp, err := h.client.Complete(ctx, openai.ChatCompletionNewParams{
		Model: shared.ChatModel(h.client.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(buildDistillSystemPrompt(req.Fields)),
			openai.UserMessage(buildDistillUserMessage(question, req.Fields, req.Docs)),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
		Temperature: CompletionTemperature(h.client.model, 0),
	}, "purpose", "distill", "documents", len(req.Docs))
	if err != nil {
		return DistillResult{}, fmt.Errorf("helper distill: %w", err)
	}
	if len(resp.Choices) == 0 {
		return DistillResult{}, fmt.Errorf("helper returned no choices")
	}

	rows, err := parseDistillRows(resp.Choices[0].Message.Content, req)
	if err != nil {
		h.client.logger.Warn("helper distill parse failed",
			"documents", len(req.Docs),
			"content_chars", len(resp.Choices[0].Message.Content),
			slog.Any("error", err),
		)
		return DistillResult{Usage: usageOf(resp)}, err
	}
	h.client.logger.Info("helper distill complete",
		"documents", len(req.Docs),
		"rows", len(rows),
		logfmt.Duration("duration", time.Since(requestStart)),
	)
	return DistillResult{Rows: rows, Usage: usageOf(resp)}, nil
}

func buildDistillSystemPrompt(fields []SurveyField) string {
	var b strings.Builder
	b.WriteString(`You read documents on behalf of a researcher and report what each one says about their question.
Use only the text you are given. Never add outside knowledge and never guess at what a gap in an excerpt might contain.
Documents are separated by lines of the form "=== document <id> ===". Answer for every document, by that id.

Return one JSON object: {"documents": [{"id": "...", "relevant": true|false, "notes": "...", "quotes": ["..."], "values": {...}, "missing": ["..."]}]}.
- relevant: whether the document bears on the question at all.
- notes: what the document says about the question, in your words, at most a few sentences. Empty when not relevant.
- quotes: up to three short verbatim passages that support the notes, copied exactly from the text.
- values: the requested fields, by name, as strings. Omit a field you cannot find and list its name in missing instead. Do not invent values.
- missing: names of requested fields the document does not contain.
- chunks: when the text carries "[chunk N]" markers, the numbers N of the chunks whose text supports the notes, as integers. Empty when not relevant or when there are no markers.
`)
	if len(fields) > 0 {
		b.WriteString("\nRequested fields:\n")
		for _, f := range fields {
			typ := strings.TrimSpace(f.Type)
			if typ == "" {
				typ = "string"
			}
			b.WriteString(fmt.Sprintf("- %s (%s)", strings.TrimSpace(f.Name), typ))
			if d := strings.TrimSpace(f.Description); d != "" {
				b.WriteString(": " + d)
			}
			b.WriteString("\n")
		}
		b.WriteString(`Number fields: a plain number with a dot as decimal separator and no thousands separator, unit or currency sign. When a currency applies, add "<name>_currency" with its ISO code.
Date fields: YYYY-MM-DD, or YYYY-MM / YYYY when the text gives no more.
`)
	}
	return b.String()
}

func buildDistillUserMessage(question string, fields []SurveyField, docs []DistillDoc) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(question)
	b.WriteString("\n")
	if len(fields) > 0 {
		names := make([]string, 0, len(fields))
		for _, f := range fields {
			names = append(names, strings.TrimSpace(f.Name))
		}
		b.WriteString("Fields to extract: ")
		b.WriteString(strings.Join(names, ", "))
		b.WriteString("\n")
	}
	for _, doc := range docs {
		b.WriteString("\n=== document ")
		b.WriteString(doc.ID)
		b.WriteString(" ===\n")
		if doc.Title != "" {
			b.WriteString("Title: " + doc.Title + "\n")
		}
		if doc.DocumentDate != "" {
			b.WriteString("Date: " + doc.DocumentDate + "\n")
		}
		if doc.DocumentType != "" {
			b.WriteString("Type: " + doc.DocumentType + "\n")
		}
		if doc.Correspondent != "" {
			b.WriteString("Correspondent: " + doc.Correspondent + "\n")
		}
		if doc.Excerpted {
			b.WriteString("(Excerpt: only the parts of the document about the question are shown; … marks gaps.)\n")
		}
		b.WriteString("\n")
		b.WriteString(doc.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// parseDistillRows reads the helper's JSON leniently (fenced, prefixed with
// reasoning, scalar kinds wrong) and keeps only rows for documents that were
// asked about. A document the helper skipped is simply absent.
func parseDistillRows(content string, req DistillRequest) ([]DistillRow, error) {
	raw := models.NormalizeJSONObject(content)
	var payload struct {
		Documents []map[string]any `json:"documents"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode helper response: %w", err)
	}
	wanted := make(map[string]struct{}, len(req.Docs))
	for _, doc := range req.Docs {
		wanted[doc.ID] = struct{}{}
	}
	fieldNames := map[string]struct{}{}
	for _, f := range req.Fields {
		fieldNames[strings.TrimSpace(f.Name)] = struct{}{}
	}

	rows := make([]DistillRow, 0, len(payload.Documents))
	seen := map[string]struct{}{}
	for _, item := range payload.Documents {
		id := strings.TrimSpace(coerceString(item["id"]))
		if _, ok := wanted[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		row := DistillRow{
			ID:       id,
			Relevant: coerceBool(item["relevant"]),
			Notes:    strings.TrimSpace(coerceString(item["notes"])),
			Quotes:   coerceStringSlice(item["quotes"]),
			Missing:  coerceStringSlice(item["missing"]),
			Chunks:   coerceIntSlice(item["chunks"]),
		}
		if values, ok := item["values"].(map[string]any); ok && len(values) > 0 {
			row.Values = make(map[string]string, len(values))
			for k, v := range values {
				k = strings.TrimSpace(k)
				s := strings.TrimSpace(coerceString(v))
				if k == "" || s == "" {
					continue
				}
				// Only requested fields and their currency companions; a
				// helper that volunteers extra columns is not wrong, but
				// the caller's totals would be.
				base := strings.TrimSuffix(k, "_currency")
				if _, ok := fieldNames[base]; !ok && len(fieldNames) > 0 {
					continue
				}
				row.Values[k] = s
			}
			if len(row.Values) == 0 {
				row.Values = nil
			}
		}
		// A note with no relevance flag is still a note: models drop the
		// boolean more often than they write false with content.
		if !row.Relevant && (row.Notes != "" || len(row.Quotes) > 0 || len(row.Values) > 0) {
			if _, present := item["relevant"]; !present {
				row.Relevant = true
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func coerceBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, _ := strconv.ParseBool(strings.TrimSpace(t))
		return b
	case float64:
		return t != 0
	default:
		return false
	}
}

// coerceIntSlice reads a list of integers the model may have written as
// numbers, numeric strings or a single scalar; duplicates and non-numbers are
// dropped and the result is sorted.
func coerceIntSlice(v any) []int {
	var items []any
	switch t := v.(type) {
	case []any:
		items = t
	case nil:
		return nil
	default:
		items = []any{t}
	}
	seen := map[int]struct{}{}
	out := make([]int, 0, len(items))
	for _, item := range items {
		var n int
		switch x := item.(type) {
		case float64:
			n = int(x)
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(x), "chunk ")))
			if err != nil {
				continue
			}
			n = parsed
		default:
			continue
		}
		if n < 0 {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Ints(out)
	return out
}

func (h *openAIHelper) Screen(ctx context.Context, req ScreenRequest) (ScreenResult, error) {
	if h.client.apiKey == "" {
		return ScreenResult{}, fmt.Errorf("AI API key is not configured")
	}
	if len(req.Docs) == 0 {
		return ScreenResult{}, nil
	}
	ctx = aiprovider.EnsureSession(ctx, "screen")
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return ScreenResult{}, fmt.Errorf("screen needs a question")
	}

	requestStart := time.Now()
	resp, err := h.client.Complete(ctx, openai.ChatCompletionNewParams{
		Model: shared.ChatModel(h.client.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(screenSystemPrompt),
			openai.UserMessage(buildScreenUserMessage(question, req.Docs)),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
		Temperature: CompletionTemperature(h.client.model, 0),
	}, "purpose", "screen", "documents", len(req.Docs))
	if err != nil {
		return ScreenResult{}, fmt.Errorf("helper screen: %w", err)
	}
	if len(resp.Choices) == 0 {
		return ScreenResult{}, fmt.Errorf("helper returned no choices")
	}
	rows, err := parseScreenRows(resp.Choices[0].Message.Content, req)
	if err != nil {
		h.client.logger.Warn("helper screen parse failed",
			"documents", len(req.Docs),
			"content_chars", len(resp.Choices[0].Message.Content),
			slog.Any("error", err),
		)
		return ScreenResult{Usage: usageOf(resp)}, err
	}
	h.client.logger.Info("helper screen complete",
		"documents", len(req.Docs),
		"rows", len(rows),
		logfmt.Duration("duration", time.Since(requestStart)),
	)
	return ScreenResult{Rows: rows, Usage: usageOf(resp)}, nil
}

// The screen sees no text, so it is told to err toward keeping: what it drops
// is never read, what it keeps costs one more helper read.
const screenSystemPrompt = `You triage documents for a researcher. For each document you see only its catalogue entry: title, date, type, correspondent, tags, people, purpose, summary, and a few passages a search matched.
Decide whether the document could contain something about the researcher's question.
Documents are separated by lines of the form "--- document <id> ---". Answer for every document, by that id.

Return one JSON object: {"documents": [{"id": "...", "verdict": "yes" | "maybe" | "no"}]}.
- yes: the entry shows the document is about the question or plainly contains what is asked.
- maybe: the document could hold something about it; the entry does not settle it.
- no: only when the document is clearly unrelated -- a different subject, party or period that cannot bear on the question.
When in doubt, say maybe. A document you leave out is treated as maybe.
`

func buildScreenUserMessage(question string, docs []ScreenDoc) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(question)
	b.WriteString("\n")
	write := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			b.WriteString(label + ": " + value + "\n")
		}
	}
	for _, doc := range docs {
		b.WriteString("\n--- document ")
		b.WriteString(doc.ID)
		b.WriteString(" ---\n")
		write("Title", doc.Title)
		if doc.TitleOriginal != doc.Title {
			write("Original title", doc.TitleOriginal)
		}
		write("Date", doc.DocumentDate)
		write("Type", doc.DocumentType)
		write("Correspondent", doc.Correspondent)
		write("Tags", strings.Join(doc.Tags, ", "))
		write("People", strings.Join(doc.People, ", "))
		if doc.PageCount > 0 {
			write("Pages", strconv.Itoa(doc.PageCount))
		}
		write("Purpose", doc.Purpose)
		write("Summary", doc.Summary)
		for _, passage := range doc.Passages {
			write("Passage", passage)
		}
	}
	return b.String()
}

// parseScreenRows keeps one verdict per asked-about document; anything but a
// clear yes or no reads as maybe.
func parseScreenRows(content string, req ScreenRequest) ([]ScreenRow, error) {
	raw := models.NormalizeJSONObject(content)
	var payload struct {
		Documents []map[string]any `json:"documents"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode helper response: %w", err)
	}
	wanted := make(map[string]struct{}, len(req.Docs))
	for _, doc := range req.Docs {
		wanted[doc.ID] = struct{}{}
	}
	rows := make([]ScreenRow, 0, len(payload.Documents))
	seen := map[string]struct{}{}
	for _, item := range payload.Documents {
		id := strings.TrimSpace(coerceString(item["id"]))
		if _, ok := wanted[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		rows = append(rows, ScreenRow{ID: id, Verdict: normalizeVerdict(item["verdict"], item["relevant"])})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func normalizeVerdict(verdict, relevant any) string {
	switch strings.ToLower(strings.TrimSpace(coerceString(verdict))) {
	case VerdictYes, "true", "relevant":
		return VerdictYes
	case VerdictNo, "false", "irrelevant":
		return VerdictNo
	case VerdictMaybe:
		return VerdictMaybe
	}
	if b, ok := relevant.(bool); ok {
		if b {
			return VerdictYes
		}
		return VerdictNo
	}
	return VerdictMaybe
}
