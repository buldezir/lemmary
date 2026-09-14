package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"

	"lemmary/backend/internal/strutil"
	"lemmary/backend/internal/websearch"
)

const (
	// maxWebCalls is the whole run's budget for reaching the public web. The
	// research loop has no round cap, and unlike the archive tools every call
	// here is billed by the provider, so the ceiling is a cost ceiling rather
	// than a context one. Ask AI counts against its own budget.
	maxWebCalls = 10

	// DefaultWebResults and MaxWebResults bound one web_search call. Small on
	// purpose: the snippets are a shortlist to fetch from, not the evidence.
	DefaultWebResults = 5
	MaxWebResults     = 10

	// MaxWebFetchURLs bounds one web_fetch call. The batch comes back as a
	// single tool message, so this and websearch's per-page cap together are
	// what keep one call from filling the context window.
	MaxWebFetchURLs = 5
)

// webBudget is a run's remaining web calls, shared by Deep Research and Ask AI
// so the two surfaces spend the same allowance the same way.
type webBudget struct{ calls int }

func (b *webBudget) claim() bool {
	if b.calls >= maxWebCalls {
		return false
	}
	b.calls++
	return true
}

type webSearchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type webFetchArgs struct {
	URLs []string `json:"urls"`
}

func webSearchTool() openai.ChatCompletionToolParam {
	return openai.ChatCompletionToolParam{
		Function: shared.FunctionDefinitionParam{
			Name: "web_search",
			Description: openai.String("Search the public web and get back ranked results with a title, a URL and a short snippet. " +
				"Use it for facts the archive cannot hold -- current prices, rates and rules, a company's present details, anything that changed after the documents were written. " +
				"A snippet is a reason to fetch the page, not the whole of what it says; use web_fetch to read one."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "What to search for. Required.",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": fmt.Sprintf("How many results to return; default %d, at most %d.", DefaultWebResults, MaxWebResults),
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

func webFetchTool() openai.ChatCompletionToolParam {
	return openai.ChatCompletionToolParam{
		Function: shared.FunctionDefinitionParam{
			Name: "web_fetch",
			Description: openai.String("Read web pages as markdown. " +
				"Pass URLs from web_search results or ones the user gave you. " +
				"Long pages are truncated, so fetch the specific page that answers the question rather than a site's front page."),
			Parameters: shared.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"urls": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": fmt.Sprintf("The http or https URLs to read; at most %d per call.", MaxWebFetchURLs),
					},
				},
				"required": []string{"urls"},
			},
		},
	}
}

func decodeWebSearchArgs(data string) (webSearchArgs, error) {
	var args webSearchArgs
	if err := json.Unmarshal([]byte(data), &args); err == nil && strings.TrimSpace(args.Query) != "" {
		return args, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return webSearchArgs{}, err
	}
	query := coerceString(raw["query"])
	if strings.TrimSpace(query) == "" {
		query = coerceString(raw["q"])
	}
	if strings.TrimSpace(query) == "" {
		return webSearchArgs{}, fmt.Errorf("no query")
	}
	return webSearchArgs{Query: query, MaxResults: coerceInt(raw["max_results"])}, nil
}

// decodeWebFetchArgs accepts the documented {"urls": [...]} shape and the forms
// models reach for anyway: a bare string, and {"url": "..."}.
func decodeWebFetchArgs(data string) (webFetchArgs, error) {
	var args webFetchArgs
	if err := json.Unmarshal([]byte(data), &args); err == nil && len(args.URLs) > 0 {
		return args, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return webFetchArgs{}, err
	}
	urls := coerceStringSlice(raw["urls"])
	if len(urls) == 0 {
		urls = coerceStringSlice(raw["url"])
	}
	if len(urls) == 0 {
		return webFetchArgs{}, fmt.Errorf("no urls")
	}
	return webFetchArgs{URLs: urls}, nil
}

// fetchableURL accepts only http and https. Tavily does the fetching, so this
// is not an SSRF guard -- it stops a scheme the provider cannot use (file:,
// data:, javascript:) from being sent as though it were a page.
func fetchableURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.Host != ""
	default:
		return false
	}
}

// runWebTool dispatches web_search and web_fetch. Shared by the research loop
// and document chat, so the two surfaces cannot drift. emit may be nil: Ask AI
// has no step stream to report into.
func runWebTool(
	ctx context.Context,
	web *websearch.Tavily,
	budget *webBudget,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	if emit == nil {
		emit = func(ResearchEvent) {}
	}
	if web == nil {
		return webError(callID, name, "web access is not configured"), false
	}
	if !budget.claim() {
		return toolExecResult{
			ID:      callID,
			Name:    name,
			Content: fmt.Sprintf(`{"error":"this run has used its %d web calls","hint":"answer from what you have gathered"}`, maxWebCalls),
		}, false
	}

	switch name {
	case "web_search":
		return runWebSearchTool(ctx, web, callID, name, argumentsJSON, emit)
	case "web_fetch":
		return runWebFetchTool(ctx, web, callID, name, argumentsJSON, emit)
	default:
		return webError(callID, name, "unknown tool: "+name), false
	}
}

func runWebSearchTool(
	ctx context.Context,
	web *websearch.Tavily,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	args, err := decodeWebSearchArgs(argumentsJSON)
	if err != nil {
		return webError(callID, name, "invalid tool arguments"), false
	}
	max := args.MaxResults
	if max <= 0 {
		max = DefaultWebResults
	}
	if max > MaxWebResults {
		max = MaxWebResults
	}

	query := strings.TrimSpace(args.Query)
	emit(ResearchEvent{Type: "step", Kind: "web_search", Status: "start", Query: query})

	results, err := web.Search(ctx, query, max)
	if err != nil {
		emit(ResearchEvent{Type: "step", Kind: "web_search", Status: "done", Query: query})
		return webError(callID, name, err.Error()), false
	}

	titles := make([]string, 0, len(results))
	encoded := make([]map[string]any, 0, len(results))
	for _, r := range results {
		titles = append(titles, strutil.FirstNonEmpty(r.Title, r.URL))
		encoded = append(encoded, map[string]any{"title": r.Title, "url": r.URL, "snippet": r.Snippet})
	}
	emit(ResearchEvent{Type: "step", Kind: "web_search", Status: "done", Query: query, Titles: titles, Count: len(results)})

	payload, err := json.Marshal(map[string]any{"count": len(results), "results": encoded})
	if err != nil {
		return webError(callID, name, "could not encode results"), false
	}
	return toolExecResult{ID: callID, Name: name, Content: string(payload)}, len(results) > 0
}

func runWebFetchTool(
	ctx context.Context,
	web *websearch.Tavily,
	callID, name, argumentsJSON string,
	emit func(ResearchEvent),
) (toolExecResult, bool) {
	args, err := decodeWebFetchArgs(argumentsJSON)
	if err != nil {
		return webError(callID, name, "invalid tool arguments"), false
	}

	wanted := make([]string, 0, len(args.URLs))
	skipped := make([]string, 0)
	seen := map[string]struct{}{}
	for _, raw := range args.URLs {
		candidate := strings.TrimSpace(raw)
		if !fetchableURL(candidate) {
			skipped = append(skipped, candidate)
			continue
		}
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		if len(wanted) < MaxWebFetchURLs {
			wanted = append(wanted, candidate)
		}
	}
	if len(wanted) == 0 {
		return toolExecResult{
			ID:      callID,
			Name:    name,
			Content: `{"error":"no fetchable urls","hint":"pass http or https urls, at most ` + fmt.Sprint(MaxWebFetchURLs) + ` per call"}`,
		}, false
	}

	emit(ResearchEvent{Type: "step", Kind: "web_fetch", Status: "start", Titles: hostsOf(wanted), Count: len(wanted)})

	pages, err := web.Fetch(ctx, wanted)
	if err != nil {
		emit(ResearchEvent{Type: "step", Kind: "web_fetch", Status: "done"})
		return webError(callID, name, err.Error()), false
	}

	read := make([]map[string]any, 0, len(pages))
	failed := make([]map[string]any, 0)
	for _, page := range pages {
		if page.Error != "" {
			failed = append(failed, map[string]any{"url": page.URL, "error": page.Error})
			continue
		}
		read = append(read, map[string]any{"url": page.URL, "content": page.Content})
	}
	emit(ResearchEvent{Type: "step", Kind: "web_fetch", Status: "done", Titles: hostsOf(wanted), Count: len(read)})

	body := map[string]any{"pages": read, "failed": failed}
	if len(skipped) > 0 {
		body["skipped_unfetchable_urls"] = skipped
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return webError(callID, name, "could not encode pages"), false
	}
	return toolExecResult{ID: callID, Name: name, Content: string(payload)}, len(read) > 0
}

func webError(callID, name, message string) toolExecResult {
	encoded, err := json.Marshal(map[string]string{"error": message})
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: `{"error":"web tool failed"}`}
	}
	return toolExecResult{ID: callID, Name: name, Content: string(encoded)}
}

func hostsOf(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, raw := range urls {
		if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
			out = append(out, parsed.Host)
			continue
		}
		out = append(out, raw)
	}
	return out
}
