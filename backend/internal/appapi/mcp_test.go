package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lemmary/backend/internal/ai"
)

func connectMCP(t *testing.T, tools agentTools, docs ...mcpDocs) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	var plain mcpDocs
	if len(docs) > 0 {
		plain = docs[0]
	}
	if _, err := newMCPServer(tools, plain).Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCPServerListsTheReadOnlyTools(t *testing.T) {
	session := connectMCP(t, agentTools{})
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	want := []string{"count_documents", "get_document", "list_documents", "list_taxonomy", "read_documents", "search_documents"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestMCPSearchPassesFiltersAndReturnsHits(t *testing.T) {
	var got ai.SearchDocumentsArgs
	tools := agentTools{
		search: func(_ context.Context, args ai.SearchDocumentsArgs) ([]ai.DocumentHit, error) {
			got = args
			return []ai.DocumentHit{{ID: "doc1", Title: "Invoice"}}, nil
		},
	}
	session := connectMCP(t, tools)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_documents",
		Arguments: map[string]any{
			"query":     "leak",
			"date_from": "2024-01-01",
			"tags":      []string{"plumbing"},
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %#v", res.Content)
	}
	if got.Query != "leak" || got.DateFrom != "2024-01-01" || !slices.Equal(got.Tags, []string{"plumbing"}) {
		t.Fatalf("args = %#v", got)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out mcpSearchResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Hits) != 1 || out.Hits[0].ID != "doc1" {
		t.Fatalf("hits = %#v", out.Hits)
	}
}

func TestMCPSearchWithoutQueryIsRefusedBySchema(t *testing.T) {
	session := connectMCP(t, agentTools{
		search: func(context.Context, ai.SearchDocumentsArgs) ([]ai.DocumentHit, error) {
			t.Fatal("search must not run without a query")
			return nil, nil
		},
	})
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search_documents",
		Arguments: map[string]any{},
	})
	if err == nil && !res.IsError {
		t.Fatalf("expected a refusal, got %#v", res)
	}
}

func TestMCPRetrieverFailureIsAToolError(t *testing.T) {
	session := connectMCP(t, agentTools{
		read: func(context.Context, ai.ReadRequest) ([]ai.DocumentContent, error) {
			return nil, errors.New("search index is not ready")
		},
	})
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "read_documents",
		Arguments: map[string]any{"ids": []string{"doc1"}},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError, got %#v", res)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "search index is not ready" {
		t.Fatalf("content = %#v", res.Content)
	}
}

func TestMCPCountWithoutDatabaseIsAToolError(t *testing.T) {
	session := connectMCP(t, agentTools{})
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "count_documents",
		Arguments: map[string]any{"group_by": "year"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError, got %#v", res)
	}
}

func TestMCPTaxonomyNeverReturnsNull(t *testing.T) {
	session := connectMCP(t, agentTools{}, mcpDocs{
		taxonomy: func(context.Context) (mcpTaxonomyResult, error) {
			return mcpTaxonomyResult{Tags: []string{"invoice"}}, nil
		},
	})
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_taxonomy"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out mcpTaxonomyResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(out.Tags, []string{"invoice"}) || out.DocumentTypes == nil || out.Correspondents == nil {
		t.Fatalf("taxonomy = %s", raw)
	}
}

func TestMCPPlainAccessListsFiltersAndReadsDocuments(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
	older := makeQueueDocument(t, app, owner, "completed", "older text")
	older.Set("document_date", "2024-01-05")
	if err := app.Save(older); err != nil {
		t.Fatal(err)
	}
	newer := makeQueueDocument(t, app, owner, "completed", strings.Repeat("newer ", 100))
	newer.Set("document_date", "2024-03-01")
	if err := app.Save(newer); err != nil {
		t.Fatal(err)
	}
	pending := makeQueueDocument(t, app, owner, "pending", "")
	foreign := makeQueueDocument(t, app, other, "completed", "not yours")

	docs := newMCPDocs(app, owner)
	ctx := context.Background()

	list, err := docs.list(ctx, mcpListArgs{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list.Total != 3 || len(list.Documents) != 3 {
		t.Fatalf("total=%d docs=%d", list.Total, len(list.Documents))
	}
	// Undated sorts by the day it was added, which is today: first.
	if list.Documents[0].ID != pending.Id || list.Documents[1].ID != newer.Id || list.Documents[2].ID != older.Id {
		t.Fatalf("order = %s %s %s", list.Documents[0].ID, list.Documents[1].ID, list.Documents[2].ID)
	}
	for _, doc := range list.Documents {
		if doc.ID == foreign.Id {
			t.Fatal("another user's document listed")
		}
	}

	list, err = docs.list(ctx, mcpListArgs{Status: "completed", Sort: "date_asc", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if list.Total != 2 || len(list.Documents) != 1 || list.Documents[0].ID != newer.Id {
		t.Fatalf("filtered = %#v", list)
	}

	list, err = docs.list(ctx, mcpListArgs{DocumentType: "no such type"})
	if err != nil || list.Total != 0 || len(list.Unresolved) != 1 {
		t.Fatalf("unresolved = %#v err=%v", list, err)
	}
	if _, err := docs.list(ctx, mcpListArgs{Sort: "sideways"}); err == nil {
		t.Fatal("expected an unknown sort to be refused")
	}

	got, err := docs.get(ctx, mcpGetArgs{ID: newer.Id, Offset: 6, MaxChars: 5})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Text != "newer" || !got.Truncated || got.TextChars != 600 || got.ProcessingStatus != "completed" {
		t.Fatalf("page = %#v", got)
	}
	got, err = docs.get(ctx, mcpGetArgs{ID: older.Id})
	if err != nil || got.Text != "older text" || got.Truncated || got.DocumentDate != "2024-01-05" {
		t.Fatalf("whole = %#v err=%v", got, err)
	}
	if _, err := docs.get(ctx, mcpGetArgs{ID: foreign.Id}); err == nil {
		t.Fatal("another user's document was readable")
	}

	tax, err := docs.taxonomy(ctx)
	if err != nil || len(tax.Tags) != 0 {
		t.Fatalf("taxonomy = %#v err=%v", tax, err)
	}
}

func TestMCPReadRefusesMoreThanTheCap(t *testing.T) {
	session := connectMCP(t, agentTools{
		read: func(context.Context, ai.ReadRequest) ([]ai.DocumentContent, error) {
			t.Fatal("read must not run past the cap")
			return nil, nil
		},
	})
	ids := make([]string, mcpMaxReadIDs+1)
	for i := range ids {
		ids[i] = "doc"
	}
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "read_documents",
		Arguments: map[string]any{"ids": ids},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError, got %#v", res)
	}
}

func TestMCPEnabledFromEnv(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := map[string]bool{
		"": false, "1": true, "true": true, "yes": true, "ON": true,
		"0": false, "false": false, "off": false, "maybe": false,
	}
	for value, want := range cases {
		t.Setenv(EnvMCPEnabled, value)
		if got := mcpEnabledFromEnv(log); got != want {
			t.Fatalf("%s=%q: got %v, want %v", EnvMCPEnabled, value, got, want)
		}
	}
}
