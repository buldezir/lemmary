package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// The two tools that let a research run cover a topic instead of a document.
//
// read_documents is right for a handful of documents. A question about a
// correspondent's whole year, or about how many of something there are, is
// not a read problem: reading two hundred documents one call at a time puts
// two hundred documents into one conversation, and counting search results
// counts a page. survey_documents hands the reading to the helper model and
// returns a row per document; count_documents asks the index and the database
// and returns a number.

// SurveyArgs is one survey_documents call after validation. Either IDs or
// Query selects the documents; Question is what the helper reads them for.
type SurveyArgs struct {
	Query         string        `json:"query,omitempty"`
	DateFrom      string        `json:"date_from,omitempty"`
	DateTo        string        `json:"date_to,omitempty"`
	DocumentType  string        `json:"document_type,omitempty"`
	Correspondent string        `json:"correspondent,omitempty"`
	Tags          []string      `json:"tags,omitempty"`
	IDs           []string      `json:"ids,omitempty"`
	Question      string        `json:"question"`
	Fields        []SurveyField `json:"fields,omitempty"`
	MaxDocuments  int           `json:"max_documents,omitempty"`
}

// searchArgs is the document selection part of a survey, in the shape the
// retriever already understands.
func (a SurveyArgs) SearchArgs() SearchDocumentsArgs {
	return SearchDocumentsArgs{
		Query:         a.Query,
		DateFrom:      a.DateFrom,
		DateTo:        a.DateTo,
		DocumentType:  a.DocumentType,
		Correspondent: a.Correspondent,
		Tags:          a.Tags,
	}
}

// SurveyRow is what the model sees of one surveyed document: compact enough
// that a thousand of them are a page of the conversation, not the archive.
type SurveyRow struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	DocumentDate string            `json:"document_date,omitempty"`
	Relevant     bool              `json:"relevant"`
	Notes        string            `json:"notes,omitempty"`
	Quote        string            `json:"quote,omitempty"`
	Values       map[string]string `json:"values,omitempty"`
	Missing      []string          `json:"missing,omitempty"`
}

// SurveyTotal is the server's arithmetic over one number field, per currency
// when the helper reported one. The model is told to report these rather
// than add the rows itself.
type SurveyTotal struct {
	Field    string  `json:"field"`
	Currency string  `json:"currency,omitempty"`
	Count    int     `json:"count"`
	Sum      float64 `json:"sum"`
	Avg      float64 `json:"avg"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
}

type SurveyResult struct {
	// Candidates is how many documents the selection matched; Surveyed how
	// many the helper read; Skipped how many were past the cap; Failed how
	// many the helper could not answer for.
	Candidates int
	Surveyed   int
	Skipped    int
	Failed     int
	Rows       []SurveyRow
	Totals     []SurveyTotal
	// Missing counts, per field, the relevant documents without a value.
	Missing map[string]int
	// Documents are the relevant ones as search hits, for the run's result
	// list: they are what the answer will cite.
	Documents []DocumentHit
}

// DocumentSurveyor runs a survey. progress is called as documents finish.
type DocumentSurveyor func(ctx context.Context, args SurveyArgs, progress func(done, total int)) (SurveyResult, error)

// CountArgs is one count_documents call after validation.
type CountArgs struct {
	Query         string   `json:"query,omitempty"`
	DateFrom      string   `json:"date_from,omitempty"`
	DateTo        string   `json:"date_to,omitempty"`
	DocumentType  string   `json:"document_type,omitempty"`
	Correspondent string   `json:"correspondent,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	// GroupBy is one of document_type, correspondent, year, month, tag.
	GroupBy string `json:"group_by,omitempty"`
}

func (a CountArgs) SearchArgs() SearchDocumentsArgs {
	return SearchDocumentsArgs{
		Query:         a.Query,
		DateFrom:      a.DateFrom,
		DateTo:        a.DateTo,
		DocumentType:  a.DocumentType,
		Correspondent: a.Correspondent,
		Tags:          a.Tags,
	}
}

type CountGroup struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type CountResult struct {
	Count   int          `json:"count"`
	GroupBy string       `json:"group_by,omitempty"`
	Groups  []CountGroup `json:"groups,omitempty"`
	// Other is the remainder past the groups shown.
	Other int `json:"other,omitempty"`
	// Unresolved lists filter values that matched no type, correspondent or
	// tag. A count of zero with a name here means "no such thing", not "none
	// of them".
	Unresolved []string `json:"unresolved_filters,omitempty"`
	// Approximate marks grouped counts over a query whose matches exceeded
	// what could be enumerated.
	Approximate bool `json:"approximate,omitempty"`
}

// DocumentCounter answers a count_documents call.
type DocumentCounter func(ctx context.Context, args CountArgs) (CountResult, error)

const (
	// DefaultSurveyDocuments is how many documents a survey reads when the
	// call does not say; MaxSurveyDocuments is the most it may ask for. The
	// rows come back as one tool result, so this bounds one message of the
	// conversation: a thousand rows is on the order of a hundred thousand
	// tokens, which the models this is built for carry.
	DefaultSurveyDocuments = 300
	MaxSurveyDocuments     = 1000
)

// ValidGroupBy is the accepted group_by set, in the order the tool lists it.
var ValidGroupBy = []string{"document_type", "correspondent", "year", "month", "tag"}

func surveyDocumentsTool() openai.ChatCompletionToolParam {
	return openai.ChatCompletionToolParam{
		Function: shared.FunctionDefinitionParam{
			Name: "survey_documents",
			Description: openai.String("Have every matching document read for one question, in parallel, and get back one compact row per document: " +
				"whether it is relevant, notes on what it says, a supporting quote, and any requested fields. " +
				"Use this for questions about many documents at once -- a topic, a correspondent's year, a total -- instead of reading them one call at a time. " +
				"Number fields are summed for you; report the server's totals rather than adding rows yourself."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "What each document should be read for. Required.",
					},
					"query": map[string]any{
						"type":        "string",
						"description": "Search terms selecting the documents to survey, with the same filters as search_documents. Either query or ids is required.",
					},
					"ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Document ids already seen in this conversation to survey instead of searching.",
					},
					"date_from":     map[string]any{"type": "string", "description": "Inclusive lower bound on document_date, YYYY-MM-DD."},
					"date_to":       map[string]any{"type": "string", "description": "Inclusive upper bound on document_date, YYYY-MM-DD."},
					"document_type": map[string]any{"type": "string", "description": "Document type name filter."},
					"correspondent": map[string]any{"type": "string", "description": "Correspondent name filter."},
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Exact tag names; documents with any of them match.",
					},
					"fields": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":        map[string]any{"type": "string"},
								"type":        map[string]any{"type": "string", "enum": []string{"string", "number", "date"}},
								"description": map[string]any{"type": "string"},
							},
							"required": []string{"name"},
						},
						"description": "Values to extract from every document, e.g. {name: \"total_amount\", type: \"number\", description: \"invoice total incl. VAT\"}. Number fields are totalled per currency.",
					},
					"max_documents": map[string]any{
						"type":        "integer",
						"description": fmt.Sprintf("How many documents to survey at most; default %d, at most %d. Narrow the filters or the date range instead of raising it when the selection is large.", DefaultSurveyDocuments, MaxSurveyDocuments),
					},
				},
				"required": []string{"question"},
			},
		},
	}
}

func countDocumentsTool() openai.ChatCompletionToolParam {
	return openai.ChatCompletionToolParam{
		Function: shared.FunctionDefinitionParam{
			Name: "count_documents",
			Description: openai.String("Count the documents matching filters, optionally grouped. " +
				"Use this for how-many and distribution questions instead of counting search results: a search result is a capped page, not the archive. " +
				"Counts are exact keyword and filter matches; for what documents say about a topic, use survey_documents."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"query":         map[string]any{"type": "string", "description": "Optional keywords every counted document must contain."},
					"date_from":     map[string]any{"type": "string", "description": "Inclusive lower bound on document_date, YYYY-MM-DD."},
					"date_to":       map[string]any{"type": "string", "description": "Inclusive upper bound on document_date, YYYY-MM-DD."},
					"document_type": map[string]any{"type": "string", "description": "Document type name filter."},
					"correspondent": map[string]any{"type": "string", "description": "Correspondent name filter."},
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Exact tag names; documents with any of them match.",
					},
					"group_by": map[string]any{
						"type":        "string",
						"enum":        ValidGroupBy,
						"description": "Break the count down by this property.",
					},
				},
			},
		},
	}
}

func decodeSurveyArgs(data string) (SurveyArgs, error) {
	var args SurveyArgs
	if err := json.Unmarshal([]byte(data), &args); err == nil {
		args.IDs = normalizeIDs(args.IDs)
		return args, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return args, err
	}
	args = SurveyArgs{
		Query:         coerceString(raw["query"]),
		DateFrom:      coerceString(raw["date_from"]),
		DateTo:        coerceString(raw["date_to"]),
		DocumentType:  coerceString(raw["document_type"]),
		Correspondent: coerceString(raw["correspondent"]),
		Tags:          coerceStringSlice(raw["tags"]),
		IDs:           normalizeIDs(coerceStringSlice(raw["ids"])),
		Question:      coerceString(raw["question"]),
		MaxDocuments:  coerceInt(raw["max_documents"]),
	}
	if fields, ok := raw["fields"].([]any); ok {
		for _, item := range fields {
			switch f := item.(type) {
			case map[string]any:
				name := strings.TrimSpace(coerceString(f["name"]))
				if name == "" {
					continue
				}
				args.Fields = append(args.Fields, SurveyField{
					Name:        name,
					Type:        coerceString(f["type"]),
					Description: coerceString(f["description"]),
				})
			case string:
				if name := strings.TrimSpace(f); name != "" {
					args.Fields = append(args.Fields, SurveyField{Name: name})
				}
			}
		}
	}
	return args, nil
}

func decodeCountArgs(data string) (CountArgs, error) {
	var args CountArgs
	if err := json.Unmarshal([]byte(data), &args); err == nil {
		return args, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return args, err
	}
	return CountArgs{
		Query:         coerceString(raw["query"]),
		DateFrom:      coerceString(raw["date_from"]),
		DateTo:        coerceString(raw["date_to"]),
		DocumentType:  coerceString(raw["document_type"]),
		Correspondent: coerceString(raw["correspondent"]),
		Tags:          coerceStringSlice(raw["tags"]),
		GroupBy:       strings.ToLower(strings.TrimSpace(coerceString(raw["group_by"]))),
	}, nil
}

// runSurveyTool dispatches survey_documents. Surveyed documents become
// readable and citable; the relevant ones join the run's results.
func (a *openAISearchAgent) runSurveyTool(
	ctx context.Context,
	req ResearchRequest,
	state *researchState,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	if req.Survey == nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"survey_documents is not available"}`}, false
	}
	args, err := decodeSurveyArgs(argumentsJSON)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"invalid tool arguments"}`}, false
	}
	args.Question = strings.TrimSpace(args.Question)
	if args.Question == "" {
		args.Question = state.question
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Question == "" {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"question is required"}`}, false
	}

	// Ids are checked the way read_documents checks them: only documents
	// this conversation has seen.
	unknown := make([]string, 0)
	if len(args.IDs) > 0 {
		wanted := make([]string, 0, len(args.IDs))
		for _, id := range args.IDs {
			if _, ok := state.seenIDs[id]; ok {
				wanted = append(wanted, id)
			} else {
				unknown = append(unknown, id)
			}
		}
		args.IDs = wanted
		if len(wanted) == 0 {
			return toolExecResult{ID: callID, Name: name, Content: `{"error":"no surveyable ids","hint":"pass ids returned by search_documents in this conversation, or a query"}`}, false
		}
	} else if args.Query == "" {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"query or ids is required"}`}, false
	}

	if args.MaxDocuments <= 0 {
		args.MaxDocuments = DefaultSurveyDocuments
	}
	if args.MaxDocuments > MaxSurveyDocuments {
		args.MaxDocuments = MaxSurveyDocuments
	}
	for i := range args.Fields {
		args.Fields[i].Name = strings.TrimSpace(args.Fields[i].Name)
		args.Fields[i].Type = strings.ToLower(strings.TrimSpace(args.Fields[i].Type))
	}

	if repeat, ok := state.claimCall(name, args); !ok {
		return toolExecResult{ID: callID, Name: name, Content: repeat}, false
	}

	label := strutilFirstNonEmpty(args.Query, args.Question)
	emit(ResearchEvent{Type: "step", Kind: "survey", Status: "start", Query: label})

	result, err := req.Survey(ctx, args, func(done, total int) {
		emit(ResearchEvent{Type: "step", Kind: "survey", Status: "progress", Query: label, Done: done, Count: total})
	})
	if err != nil {
		emit(ResearchEvent{Type: "step", Kind: "survey", Status: "done", Query: label})
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, false
	}

	newDocs := 0
	for _, row := range result.Rows {
		if row.ID == "" {
			continue
		}
		state.titles[row.ID] = row.Title
		if _, seen := state.seenIDs[row.ID]; !seen {
			state.seenIDs[row.ID] = struct{}{}
			newDocs++
		}
	}
	inHits := make(map[string]struct{}, len(state.hits))
	for _, hit := range state.hits {
		inHits[hit.ID] = struct{}{}
	}
	for _, hit := range result.Documents {
		if _, ok := inHits[hit.ID]; ok || hit.ID == "" {
			continue
		}
		inHits[hit.ID] = struct{}{}
		state.hits = append(state.hits, hit)
	}
	for _, row := range result.Rows {
		state.read[row.ID] = struct{}{}
	}

	emit(ResearchEvent{Type: "step", Kind: "survey", Status: "done", Query: label, Count: result.Surveyed})

	payload := map[string]any{
		"question":       args.Question,
		"candidates":     result.Candidates,
		"surveyed":       result.Surveyed,
		"count_relevant": countRelevant(result.Rows),
		"rows":           result.Rows,
	}
	if result.Skipped > 0 {
		payload["skipped"] = result.Skipped
		payload["hint"] = "more documents matched than were surveyed; narrow the filters or date range, or call again with max_documents"
	}
	if result.Failed > 0 {
		payload["failed"] = result.Failed
	}
	if len(result.Totals) > 0 {
		payload["totals"] = result.Totals
	}
	if len(result.Missing) > 0 {
		payload["missing_values"] = result.Missing
	}
	if len(unknown) > 0 {
		payload["skipped_unknown_ids"] = unknown
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"failed to encode survey"}`}, false
	}
	// A survey is progress when it read anything at all: even a re-survey of
	// known documents with a new question is new evidence.
	return toolExecResult{ID: callID, Name: name, Content: string(encoded)}, result.Surveyed > 0 || newDocs > 0
}

func countRelevant(rows []SurveyRow) int {
	n := 0
	for _, row := range rows {
		if row.Relevant {
			n++
		}
	}
	return n
}

// runCountTool dispatches count_documents.
func (a *openAISearchAgent) runCountTool(
	ctx context.Context,
	req ResearchRequest,
	state *researchState,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	if req.Count == nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"count_documents is not available"}`}, false
	}
	args, err := decodeCountArgs(argumentsJSON)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"invalid tool arguments"}`}, false
	}
	args.GroupBy = strings.ToLower(strings.TrimSpace(args.GroupBy))
	if args.GroupBy != "" && !validGroupBy(args.GroupBy) {
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":"unknown group_by %q","allowed":%q}`, args.GroupBy, strings.Join(ValidGroupBy, ", "))}, false
	}
	if repeat, ok := state.claimCall(name, args); !ok {
		return toolExecResult{ID: callID, Name: name, Content: repeat}, false
	}

	label := strings.TrimSpace(args.Query)
	emit(ResearchEvent{Type: "step", Kind: "count", Status: "start", Query: label})
	result, err := req.Count(ctx, args)
	if err != nil {
		emit(ResearchEvent{Type: "step", Kind: "count", Status: "done", Query: label})
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, false
	}
	emit(ResearchEvent{Type: "step", Kind: "count", Status: "done", Query: label, Count: result.Count})

	encoded, err := json.Marshal(result)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"failed to encode count"}`}, false
	}
	// A count is a new fact whenever it is a new call; claimCall has already
	// refused the repeats.
	return toolExecResult{ID: callID, Name: name, Content: string(encoded)}, true
}

func validGroupBy(v string) bool {
	for _, g := range ValidGroupBy {
		if g == v {
			return true
		}
	}
	return false
}

func strutilFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// sortRows orders survey rows relevant-first, then by date, then title, so
// the model reads the evidence before the noise.
func sortRows(rows []SurveyRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Relevant != rows[j].Relevant {
			return rows[i].Relevant
		}
		if rows[i].DocumentDate != rows[j].DocumentDate {
			return rows[i].DocumentDate < rows[j].DocumentDate
		}
		return rows[i].Title < rows[j].Title
	})
}

// SortSurveyRows is sortRows for callers outside the package.
func SortSurveyRows(rows []SurveyRow) { sortRows(rows) }
