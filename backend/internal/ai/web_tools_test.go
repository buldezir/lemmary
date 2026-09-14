package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lemmary/backend/internal/websearch"
)

// newWebServer answers /search and /extract with fixed bodies and counts calls,
// so a test can assert that a guard kept the provider out of it.
func newWebServer(t *testing.T, searchBody, extractBody string) (*websearch.Tavily, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/extract") {
			_, _ = io.WriteString(w, extractBody)
			return
		}
		_, _ = io.WriteString(w, searchBody)
	}))
	t.Cleanup(server.Close)
	return websearch.NewTavily("k", server.URL, 5*time.Second, nil), calls
}

func decodeToolContent(t *testing.T, content string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		t.Fatalf("tool content is not JSON: %v (%s)", err, content)
	}
	return out
}

func TestDecodeWebSearchArgsAcceptsTheShapesModelsSend(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		data       string
		wantQuery  string
		wantMax    int
		wantFailed bool
	}{
		{"documented", `{"query":"vat rate","max_results":7}`, "vat rate", 7, false},
		{"no max", `{"query":"vat rate"}`, "vat rate", 0, false},
		// Models get JSON scalar types wrong; coerceInt is why this survives.
		{"stringly typed max", `{"query":"vat rate","max_results":"7"}`, "vat rate", 7, false},
		{"q instead of query", `{"q":"vat rate"}`, "vat rate", 0, false},
		{"blank query", `{"query":"   "}`, "", 0, true},
		{"not json", `nonsense`, "", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := decodeWebSearchArgs(tc.data)
			if tc.wantFailed {
				if err == nil {
					t.Fatalf("decodeWebSearchArgs(%q) should fail", tc.data)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeWebSearchArgs(%q) error = %v", tc.data, err)
			}
			if strings.TrimSpace(args.Query) != tc.wantQuery {
				t.Errorf("Query = %q, want %q", args.Query, tc.wantQuery)
			}
			if args.MaxResults != tc.wantMax {
				t.Errorf("MaxResults = %d, want %d", args.MaxResults, tc.wantMax)
			}
		})
	}
}

func TestDecodeWebFetchArgsAcceptsTheShapesModelsSend(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
		want []string
	}{
		{"documented", `{"urls":["https://a.example","https://b.example"]}`, []string{"https://a.example", "https://b.example"}},
		{"singular key", `{"url":"https://a.example"}`, []string{"https://a.example"}},
		{"bare string", `{"urls":"https://a.example"}`, []string{"https://a.example"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := decodeWebFetchArgs(tc.data)
			if err != nil {
				t.Fatalf("decodeWebFetchArgs(%q) error = %v", tc.data, err)
			}
			if len(args.URLs) != len(tc.want) {
				t.Fatalf("URLs = %v, want %v", args.URLs, tc.want)
			}
			for i, want := range tc.want {
				if args.URLs[i] != want {
					t.Errorf("URLs[%d] = %q, want %q", i, args.URLs[i], want)
				}
			}
		})
	}
	if _, err := decodeWebFetchArgs(`{"urls":[]}`); err == nil {
		t.Error("decodeWebFetchArgs with no urls should fail")
	}
}

func TestFetchableURLAcceptsOnlyHTTPAndHTTPS(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"https://example.com", "http://example.com/a?b=c", "  https://example.com  "} {
		if !fetchableURL(raw) {
			t.Errorf("fetchableURL(%q) = false", raw)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "data:text/html,x", "javascript:alert(1)", "ftp://example.com", "example.com", "https://", ""} {
		if fetchableURL(raw) {
			t.Errorf("fetchableURL(%q) = true", raw)
		}
	}
}

func TestWebSearchReturnsResultsAndEmitsAStep(t *testing.T) {
	web, calls := newWebServer(t, `{"results":[{"title":"VAT","url":"https://example.com/vat","content":"19%"}]}`, `{}`)

	var events []ResearchEvent
	result, advanced := runWebTool(context.Background(), web, &webBudget{}, "c1", "web_search",
		`{"query":"vat rate"}`, func(e ResearchEvent) { events = append(events, e) })

	if !advanced {
		t.Error("a search that found something is progress")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("provider calls = %d, want 1", got)
	}
	body := decodeToolContent(t, result.Content)
	if got := body["count"]; got != float64(1) {
		t.Errorf("count = %v, want 1", got)
	}
	results, _ := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("results = %v", body["results"])
	}
	first, _ := results[0].(map[string]any)
	if got := first["url"]; got != "https://example.com/vat" {
		t.Errorf("url = %v", got)
	}

	if len(events) != 2 {
		t.Fatalf("events = %d, want a start and a done", len(events))
	}
	if got := events[0]; got.Kind != "web_search" || got.Status != "start" || got.Query != "vat rate" {
		t.Errorf("start event = %+v", got)
	}
	if got := events[1]; got.Status != "done" || got.Count != 1 || len(got.Titles) != 1 {
		t.Errorf("done event = %+v", got)
	}
}

// Ask AI has no step stream, so the executor has to take a nil emit.
func TestWebToolsAcceptANilEmit(t *testing.T) {
	web, _ := newWebServer(t, `{"results":[]}`, `{}`)
	if _, advanced := runWebTool(context.Background(), web, &webBudget{}, "c1", "web_search", `{"query":"x"}`, nil); advanced {
		t.Error("a search that found nothing is not progress")
	}
}

func TestWebSearchCapsTheResultCount(t *testing.T) {
	calls := &atomic.Int64{}
	var asked float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		asked, _ = body["max_results"].(float64)
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	t.Cleanup(server.Close)
	web := websearch.NewTavily("k", server.URL, 5*time.Second, nil)

	runWebTool(context.Background(), web, &webBudget{}, "c1", "web_search", `{"query":"x","max_results":500}`, nil)
	if int(asked) != MaxWebResults {
		t.Errorf("max_results = %v, want it capped at %d", asked, MaxWebResults)
	}
}

func TestWebFetchSkipsUnfetchableURLsAndKeepsTheRest(t *testing.T) {
	web, _ := newWebServer(t, `{}`, `{"results":[{"url":"https://example.com/a","raw_content":"body"}]}`)

	result, advanced := runWebTool(context.Background(), web, &webBudget{}, "c1", "web_fetch",
		`{"urls":["https://example.com/a","file:///etc/passwd"]}`, nil)

	if !advanced {
		t.Error("a fetch that read a page is progress")
	}
	body := decodeToolContent(t, result.Content)
	pages, _ := body["pages"].([]any)
	if len(pages) != 1 {
		t.Fatalf("pages = %v", body["pages"])
	}
	skipped, _ := body["skipped_unfetchable_urls"].([]any)
	if len(skipped) != 1 || skipped[0] != "file:///etc/passwd" {
		t.Errorf("skipped = %v, want the file: url reported back", body["skipped_unfetchable_urls"])
	}
}

func TestWebFetchRefusesWhenNoURLIsFetchable(t *testing.T) {
	web, calls := newWebServer(t, `{}`, `{"results":[]}`)

	result, advanced := runWebTool(context.Background(), web, &webBudget{}, "c1", "web_fetch",
		`{"urls":["file:///etc/passwd","javascript:alert(1)"]}`, nil)

	if advanced {
		t.Error("nothing was read")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider calls = %d, want the provider left alone", got)
	}
	if got := decodeToolContent(t, result.Content)["error"]; got != "no fetchable urls" {
		t.Errorf("error = %v", got)
	}
}

func TestWebFetchBoundsOneCallsURLList(t *testing.T) {
	var asked int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URLs []string `json:"urls"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		asked = len(body.URLs)
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	t.Cleanup(server.Close)
	web := websearch.NewTavily("k", server.URL, 5*time.Second, nil)

	urls := make([]string, 0, MaxWebFetchURLs+3)
	for i := 0; i < MaxWebFetchURLs+3; i++ {
		urls = append(urls, "https://example.com/"+string(rune('a'+i)))
	}
	encoded, err := json.Marshal(webFetchArgs{URLs: urls})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	runWebTool(context.Background(), web, &webBudget{}, "c1", "web_fetch", string(encoded), nil)
	if asked != MaxWebFetchURLs {
		t.Errorf("urls sent = %d, want %d", asked, MaxWebFetchURLs)
	}
}

// The research loop has no round cap, so the budget is the only thing between a
// determined model and an unbounded bill.
func TestTheRunsWebBudgetIsSpentOnceAndThenRefused(t *testing.T) {
	web, calls := newWebServer(t, `{"results":[{"title":"t","url":"https://example.com/a","content":"c"}]}`, `{}`)
	budget := &webBudget{}

	for i := 0; i < maxWebCalls; i++ {
		if _, refused := decodeToolContent(t, mustContent(t, web, budget))["error"]; refused {
			t.Fatalf("call %d was refused while the budget still had room", i)
		}
	}
	if got := calls.Load(); got != int64(maxWebCalls) {
		t.Fatalf("provider calls = %d, want %d", got, maxWebCalls)
	}

	result, advanced := runWebTool(context.Background(), web, budget, "c1", "web_search", `{"query":"x"}`, nil)
	if advanced {
		t.Error("a refused call is not progress")
	}
	if got := calls.Load(); got != int64(maxWebCalls) {
		t.Errorf("provider calls = %d, want the provider left alone once the budget is gone", got)
	}
	message, _ := decodeToolContent(t, result.Content)["error"].(string)
	if !strings.Contains(message, "web calls") {
		t.Errorf("error = %q, want it to say the budget is gone", message)
	}
}

func mustContent(t *testing.T, web *websearch.Tavily, budget *webBudget) string {
	t.Helper()
	result, _ := runWebTool(context.Background(), web, budget, "c1", "web_search", `{"query":"x"}`, nil)
	return result.Content
}

func TestWebToolsRefuseWhenNoProviderIsBound(t *testing.T) {
	t.Parallel()
	budget := &webBudget{}
	result, advanced := runWebTool(context.Background(), nil, budget, "c1", "web_search", `{"query":"x"}`, nil)
	if advanced {
		t.Error("nothing happened")
	}
	if got := decodeToolContent(t, result.Content)["error"]; got != "web access is not configured" {
		t.Errorf("error = %v", got)
	}
	// A refusal must not spend the budget it never used.
	if budget.calls != 0 {
		t.Errorf("budget spent = %d, want 0", budget.calls)
	}
}

func TestAnUnknownWebToolNameIsReportedBack(t *testing.T) {
	web, _ := newWebServer(t, `{"results":[]}`, `{}`)
	result, _ := runWebTool(context.Background(), web, &webBudget{}, "c1", "web_crawl", `{}`, nil)
	message, _ := decodeToolContent(t, result.Content)["error"].(string)
	if !strings.Contains(message, "web_crawl") {
		t.Errorf("error = %q, want it to name the tool", message)
	}
}

// The tools are declared only when a provider is bound, so an archive-only run
// cannot be talked into reaching the web.
func TestWebToolsAreOfferedOnlyWithAProvider(t *testing.T) {
	t.Parallel()
	names := func(req ResearchRequest) []string {
		tools := researchTools()
		if req.Web != nil {
			tools = append(tools, webSearchTool(), webFetchTool())
		}
		out := make([]string, 0, len(tools))
		for _, tool := range tools {
			out = append(out, tool.Function.Name)
		}
		return out
	}
	without := strings.Join(names(ResearchRequest{}), ",")
	if strings.Contains(without, "web_") {
		t.Errorf("tools without a provider = %s", without)
	}
	with := strings.Join(names(ResearchRequest{Web: websearch.NewTavily("k", "", time.Second, nil)}), ",")
	if !strings.Contains(with, "web_search") || !strings.Contains(with, "web_fetch") {
		t.Errorf("tools with a provider = %s", with)
	}
}
