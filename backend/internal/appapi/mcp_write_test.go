package appapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/reprocess"
)

func connectMCPWrites(t *testing.T, app core.App, userID string, capabilities ...string) *mcp.ClientSession {
	t.Helper()
	server := newMCPServer(agentTools{}, newMCPDocs(app, userID))
	addMCPWriteTools(server, app, userID, capabilities)
	return connectMCPServer(t, server)
}

// callMCP decodes a successful call's structured result into out.
func callMCP(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
	}
	return res
}

func mcpToolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestMCPWriteToolsFollowCapabilities(t *testing.T) {
	writeTools := map[string][]string{
		config.MCPEdit:      {"update_document"},
		config.MCPReprocess: {"reprocess_documents"},
		config.MCPUpload:    {"upload_document"},
		config.MCPDelete:    {"delete_documents"},
		config.MCPTags:      {"create_tag", "rename_tag", "delete_tag"},
	}
	all := []string{"update_document", "reprocess_documents", "upload_document", "delete_documents", "create_tag", "rename_tag", "delete_tag"}

	names := mcpToolNames(t, connectMCPWrites(t, nil, ""))
	for _, name := range all {
		if slices.Contains(names, name) {
			t.Fatalf("read-only server offers %s", name)
		}
	}

	for capability, tools := range writeTools {
		names := mcpToolNames(t, connectMCPWrites(t, nil, "", capability))
		for _, name := range all {
			if slices.Contains(names, name) != slices.Contains(tools, name) {
				t.Fatalf("%s: tools = %v", capability, names)
			}
		}
	}
}

func TestMCPUpdateDocumentChangesOnlyOwnDocuments(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
	document := makeQueueDocument(t, app, owner, models.DocStatusNeedsReview, "text")
	makeQueueTag(t, app, owner, "tax")
	makeQueueTag(t, app, other, "other-tag")
	foreign := makeQueueDocument(t, app, other, models.DocStatusCompleted, "not yours")
	shares, err := app.FindCollectionByNameOrId(CollectionShares)
	if err != nil {
		t.Fatal(err)
	}
	grant := core.NewRecord(shares)
	grant.Set("document", foreign.Id)
	grant.Set("user", owner)
	if err := app.Save(grant); err != nil {
		t.Fatal(err)
	}
	session := connectMCPWrites(t, app, owner, config.MCPEdit)

	var got mcpDocument
	res := callMCP(t, session, "update_document", map[string]any{
		"id":            document.Id,
		"title":         "Electricity bill",
		"document_date": "2024-02-03",
		"document_type": "Invoice",
		"correspondent": "Power Co",
		"tags":          []string{"tax"},
		"mark_reviewed": true,
	}, &got)
	if res.IsError {
		t.Fatalf("update: %#v", res.Content)
	}
	if got.Title != "Electricity bill" || got.DocumentDate != "2024-02-03" || got.DocumentType != "Invoice" ||
		got.Correspondent != "Power Co" || !slices.Equal(got.Tags, []string{"tax"}) || got.ProcessingStatus != models.DocStatusCompleted {
		t.Fatalf("updated = %#v", got)
	}
	stored, err := app.FindRecordById("documents", document.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GetString("metadata_source") != models.MetadataSourceUser {
		t.Fatalf("metadata_source = %q", stored.GetString("metadata_source"))
	}

	var cleared mcpDocument
	res = callMCP(t, session, "update_document", map[string]any{"id": document.Id, "document_type": ""}, &cleared)
	if res.IsError || cleared.DocumentType != "" || cleared.Title != "Electricity bill" {
		t.Fatalf("clear type = %#v %#v", cleared, res.Content)
	}

	for name, args := range map[string]map[string]any{
		"shared document":   {"id": foreign.Id, "title": "mine now"},
		"another user's tag": {"id": document.Id, "tags": []string{"other-tag"}},
		"bad date":          {"id": document.Id, "document_date": "03.02.2024"},
	} {
		if res := callMCP(t, session, "update_document", args, nil); !res.IsError {
			t.Fatalf("%s: expected IsError", name)
		}
	}
	if stored, _ := app.FindRecordById("documents", foreign.Id); stored.GetString("title") != "Queue test" {
		t.Fatalf("shared document changed: %q", stored.GetString("title"))
	}
}

func TestMCPUploadStoresAPendingDocument(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	session := connectMCPWrites(t, app, owner, config.MCPUpload)

	// What tools actually send: the base64 CLI wraps at 76 columns, some
	// encoders drop the padding, browsers hand over data: URLs. Each body is a
	// different file, or the duplicate check would refuse the second.
	for i, encoded := range []string{
		base64.StdEncoding.EncodeToString([]byte("pay the plumber")),
		"cGF5IHRoZSBwbHVt\nYmVyIGFnYWlu\n",
		base64.RawStdEncoding.EncodeToString([]byte("pay the plumber, third time")),
		"data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("pay the plumber, finally")),
	} {
		var got mcpDocument
		res := callMCP(t, session, "upload_document", map[string]any{"filename": "note.txt", "content_base64": encoded}, &got)
		if res.IsError {
			t.Fatalf("upload %d: %#v", i, res.Content)
		}
		stored, err := app.FindRecordById("documents", got.ID)
		if err != nil {
			t.Fatalf("uploaded document %d: %v", i, err)
		}
		if stored.GetString("user") != owner || stored.GetString("processing_status") != models.DocStatusPending ||
			stored.GetString("file") == "" {
			t.Fatalf("stored %d = %v", i, stored.PublicExport())
		}
	}

	for name, args := range map[string]map[string]any{
		"not base64": {"filename": "a.txt", "content_base64": "%%%"},
		"zip":        {"filename": "a.zip", "content_base64": base64.StdEncoding.EncodeToString([]byte("PK\x03\x04zipzipzip"))},
	} {
		if res := callMCP(t, session, "upload_document", args, nil); !res.IsError {
			t.Fatalf("%s: expected IsError", name)
		}
	}
	if _, err := decodeMCPFile(base64.StdEncoding.EncodeToString(make([]byte, mcpMaxUploadBytes+1))); err == nil {
		t.Fatal("a file over the cap was accepted")
	}
}

func TestMCPDeleteRemovesOnlyOwnDocuments(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
	mine := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "")
	foreign := makeQueueDocument(t, app, other, models.DocStatusCompleted, "")
	session := connectMCPWrites(t, app, owner, config.MCPDelete)

	var got mcpDeleteResult
	callMCP(t, session, "delete_documents", map[string]any{"ids": []string{mine.Id, foreign.Id}}, &got)
	if !slices.Equal(got.Deleted, []string{mine.Id}) || !slices.Equal(got.NotFound, []string{foreign.Id}) {
		t.Fatalf("result = %#v", got)
	}
	if _, err := app.FindRecordById("documents", mine.Id); err == nil {
		t.Fatal("own document still there")
	}
	if _, err := app.FindRecordById("documents", foreign.Id); err != nil {
		t.Fatalf("another user's document deleted: %v", err)
	}
}

func TestMCPReprocessQueuesOwnDocuments(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
	failed := makeQueueDocument(t, app, owner, models.DocStatusFailed, "text")
	foreign := makeQueueDocument(t, app, other, models.DocStatusFailed, "text")
	session := connectMCPWrites(t, app, owner, config.MCPReprocess)

	var got reprocess.Result
	res := callMCP(t, session, "reprocess_documents", map[string]any{"ids": []string{failed.Id, foreign.Id}, "mode": "extraction"}, &got)
	if res.IsError || got.Queued != 1 || got.Skipped != 1 {
		t.Fatalf("result = %#v %#v", got, res.Content)
	}
	if jobs, _ := app.FindRecordsByFilter("processing_jobs", "document = {:id}", "", 0, 0, map[string]any{"id": foreign.Id}); len(jobs) != 0 {
		t.Fatalf("another user's document was queued: %d jobs", len(jobs))
	}
	if res := callMCP(t, session, "reprocess_documents", map[string]any{"ids": []string{failed.Id}, "mode": "sideways"}, nil); !res.IsError {
		t.Fatal("expected an unknown mode to be refused")
	}
}

func TestMCPTagToolsCreateRenameAndDelete(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
	makeQueueTag(t, app, other, "theirs")
	document := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "")
	session := connectMCPWrites(t, app, owner, config.MCPTags)

	var tag mcpTagResult
	callMCP(t, session, "create_tag", map[string]any{"name": "tax"}, &tag)
	if !tag.Created || tag.Name != "tax" {
		t.Fatalf("create = %#v", tag)
	}
	created := tag.ID
	var again mcpTagResult
	callMCP(t, session, "create_tag", map[string]any{"name": "tax"}, &again)
	if again.Created || again.ID != created {
		t.Fatalf("create again = %#v", again)
	}

	document.Set("tags", []string{created})
	if err := app.Save(document); err != nil {
		t.Fatal(err)
	}
	callMCP(t, session, "rename_tag", map[string]any{"name": "tax", "new_name": "taxes"}, &tag)
	if stored, _ := app.FindRecordById("tags", created); stored.GetString("name") != "taxes" {
		t.Fatalf("renamed = %q", stored.GetString("name"))
	}
	if res := callMCP(t, session, "rename_tag", map[string]any{"name": "theirs", "new_name": "mine"}, nil); !res.IsError {
		t.Fatal("renamed another user's tag")
	}

	callMCP(t, session, "delete_tag", map[string]any{"name": "taxes"}, &tag)
	if _, err := app.FindRecordById("tags", created); err == nil {
		t.Fatal("tag still there")
	}
	if stored, _ := app.FindRecordById("documents", document.Id); len(stored.GetStringSlice("tags")) != 0 {
		t.Fatalf("document still tagged: %v", stored.GetStringSlice("tags"))
	}
}
