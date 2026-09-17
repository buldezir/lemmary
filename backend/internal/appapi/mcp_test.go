package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lemmary/backend/internal/ai"
)

func connectMCP(t *testing.T, tools agentTools) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := newMCPServer(tools).Connect(ctx, serverTransport, nil); err != nil {
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
	want := []string{"count_documents", "list_tags", "read_documents", "search_documents"}
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

func TestMCPListTagsReturnsTheBoundNames(t *testing.T) {
	session := connectMCP(t, agentTools{tags: []string{"invoice", "plumbing"}})
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_tags"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out mcpTagsResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(out.Tags, []string{"invoice", "plumbing"}) {
		t.Fatalf("tags = %v", out.Tags)
	}
}
