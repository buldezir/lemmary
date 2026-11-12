package appapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/reprocess"
)

func connectMCPWrites(t *testing.T, app core.App, userID string, capabilities ...string) *mcp.ClientSession {
	t.Helper()
	server := newMCPServer(agentTools{}, newMCPDocs(app, userID, ""))
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
		config.MCPOCR:       {"edit_document_text"},
		config.MCPReprocess: {"reprocess_documents"},
		config.MCPUpload:    {"upload_document"},
		config.MCPDelete:    {"delete_documents"},
		config.MCPTags:      {"create_tag", "rename_tag", "delete_tag"},
	}
	all := []string{"update_document", "edit_document_text", "reprocess_documents", "upload_document", "delete_documents", "create_tag", "rename_tag", "delete_tag"}

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

func TestMCPReadsAndEditsCustomFieldsByName(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	document := makeQueueDocument(t, app, owner, models.DocStatusNeedsReview, "text")
	fields, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for name, kind := range map[string]string{"Amount": models.CustomFieldNumber, "Status": models.CustomFieldChoice, "Invoice number": models.CustomFieldText} {
		field := core.NewRecord(fields)
		field.Set("name", name)
		field.Set("type", kind)
		if err := app.Save(field); err != nil {
			t.Fatal(err)
		}
		ids[name] = field.Id
	}
	choices, err := app.FindCollectionByNameOrId(models.CustomFieldChoicesCollection)
	if err != nil {
		t.Fatal(err)
	}
	paid := core.NewRecord(choices)
	paid.Set("field", ids["Status"])
	paid.Set("name", "Paid")
	if err := app.Save(paid); err != nil {
		t.Fatal(err)
	}
	session := connectMCPWrites(t, app, owner, config.MCPEdit)

	var tax mcpTaxonomyResult
	callMCP(t, session, "list_taxonomy", nil, &tax)
	i := slices.IndexFunc(tax.Fields, func(f mcpTaxonomyField) bool { return f.Name == "Status" })
	if len(tax.Fields) != 3 || i < 0 || tax.Fields[i].Type != models.CustomFieldChoice || !slices.Equal(tax.Fields[i].Choices, []string{"Paid"}) {
		t.Fatalf("taxonomy fields = %#v", tax.Fields)
	}

	var got mcpDocument
	res := callMCP(t, session, "update_document", map[string]any{
		"id":            document.Id,
		"fields":        map[string]any{"amount": "12.50", "Status": "paid", "Invoice number": "INV-7"},
		"document_type": "Invoice",
	}, &got)
	if res.IsError {
		t.Fatalf("update: %#v", res.Content)
	}
	want := map[string]any{"Amount": 12.5, "Status": "Paid", "Invoice number": "INV-7"}
	if !maps.Equal(got.Fields, want) || got.DocumentType != "Invoice" {
		t.Fatalf("fields = %#v, type = %q", got.Fields, got.DocumentType)
	}
	if stored, _ := app.FindRecordById("documents", document.Id); stored.GetString("metadata_source") != models.MetadataSourceUser {
		t.Fatalf("metadata_source = %q", stored.GetString("metadata_source"))
	}
	var read mcpGetResult
	callMCP(t, session, "get_document", map[string]any{"id": document.Id}, &read)
	if !maps.Equal(read.Fields, want) {
		t.Fatalf("get_document fields = %#v", read.Fields)
	}

	for name, value := range map[string]map[string]any{
		"an unknown field":        {"Due": "2024-01-01"},
		"an unlisted choice":      {"Status": "Overdue"},
		"a word for a number":     {"Amount": "twelve"},
		"an option field by name": {"Correspondent": "Acme"},
	} {
		if res := callMCP(t, session, "update_document", map[string]any{"id": document.Id, "fields": value}, nil); !res.IsError {
			t.Fatalf("%s: expected IsError", name)
		}
	}

	var cleared mcpDocument
	callMCP(t, session, "update_document", map[string]any{"id": document.Id, "fields": map[string]any{"Amount": nil}}, &cleared)
	if _, ok := cleared.Fields["Amount"]; ok || cleared.Fields["Status"] != "Paid" {
		t.Fatalf("after clearing Amount: %#v", cleared.Fields)
	}
}

func TestMCPEditDocumentTextAppliesAllEditsOrNone(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
	document := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "Tota1 due: 12O EUR")
	foreign := makeQueueDocument(t, app, other, models.DocStatusCompleted, "not yours")
	session := connectMCPWrites(t, app, owner, config.MCPOCR)
	text := func(id string) string {
		t.Helper()
		record, err := app.FindRecordById("documents", id)
		if err != nil {
			t.Fatal(err)
		}
		return record.GetString("ocr_text")
	}

	var got mcpTextResult
	res := callMCP(t, session, "edit_document_text", map[string]any{"id": document.Id, "edits": []map[string]any{
		{"old_text": "Tota1", "new_text": "Total"},
		{"old_text": "12O", "new_text": "120"},
	}}, &got)
	if res.IsError || text(document.Id) != "Total due: 120 EUR" || got.TextChars != 18 {
		t.Fatalf("edits: %q, %#v, %#v", text(document.Id), got, res.Content)
	}

	for name, args := range map[string]map[string]any{
		"a missing passage":   {"id": document.Id, "edits": []map[string]any{{"old_text": "Total", "new_text": "Sum"}, {"old_text": "nowhere", "new_text": "x"}}},
		"a repeated passage":  {"id": document.Id, "edits": []map[string]any{{"old_text": " ", "new_text": "_"}}},
		"text and edits":      {"id": document.Id, "text": "x", "edits": []map[string]any{{"old_text": "Total", "new_text": "Sum"}}},
		"neither":             {"id": document.Id},
		"another user's text": {"id": foreign.Id, "text": "mine now"},
	} {
		if res := callMCP(t, session, "edit_document_text", args, nil); !res.IsError {
			t.Fatalf("%s: expected IsError", name)
		}
	}
	if text(document.Id) != "Total due: 120 EUR" || text(foreign.Id) != "not yours" {
		t.Fatalf("a refused edit changed the text: %q, %q", text(document.Id), text(foreign.Id))
	}

	callMCP(t, session, "edit_document_text", map[string]any{"id": document.Id, "text": "Rewritten"}, nil)
	if text(document.Id) != "Rewritten" {
		t.Fatalf("whole text: %q", text(document.Id))
	}
}

// Marking reviewed alone is not an edit: the model still wrote the metadata,
// and an untitled duplicate reviewed this way still gets extracted.
func TestMCPMarkReviewedAloneKeepsTheMetadataSource(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	document := makeQueueDocument(t, app, owner, models.DocStatusNeedsReview, "text")
	document.Set("metadata_source", "test-model")
	if err := app.Save(document); err != nil {
		t.Fatal(err)
	}
	session := connectMCPWrites(t, app, owner, config.MCPEdit)

	if res := callMCP(t, session, "update_document", map[string]any{"id": document.Id, "mark_reviewed": true}, nil); res.IsError {
		t.Fatalf("mark reviewed: %#v", res.Content)
	}
	stored, err := app.FindRecordById("documents", document.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GetString("metadata_source") != "test-model" || stored.GetString("processing_status") != models.DocStatusCompleted {
		t.Fatalf("metadata_source = %q, status = %q", stored.GetString("metadata_source"), stored.GetString("processing_status"))
	}
}

func TestMCPMarkReviewedKeepsAFailedDocumentAsItIs(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	failed := makeQueueDocument(t, app, owner, models.DocStatusFailed, "text")
	pending := makeQueueDocument(t, app, owner, models.DocStatusPending, "text")
	session := connectMCPWrites(t, app, owner, config.MCPEdit)

	var got mcpDocument
	callMCP(t, session, "update_document", map[string]any{"id": failed.Id, "mark_reviewed": true}, &got)
	if got.ProcessingStatus != models.DocStatusCompleted {
		t.Fatalf("failed: status = %q", got.ProcessingStatus)
	}
	callMCP(t, session, "update_document", map[string]any{"id": pending.Id, "mark_reviewed": true}, &got)
	if got.ProcessingStatus != models.DocStatusPending {
		t.Fatalf("pending: status = %q", got.ProcessingStatus)
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

func TestMCPUploadAtTheCapFitsTheHTTPBody(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	server := newMCPServer(agentTools{}, newMCPDocs(app, owner, ""))
	addMCPWriteTools(server, app, owner, []string{config.MCPUpload})
	srv := httptest.NewServer(mcpHTTPHandler(server))
	t.Cleanup(srv.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: srv.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Wrapped at 76 columns like the base64 CLI: each break is escaped in the
	// JSON, which is the largest body a file at the cap arrives in.
	flat := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), mcpMaxUploadBytes))
	var wrapped strings.Builder
	for len(flat) > 76 {
		wrapped.WriteString(flat[:76] + "\n")
		flat = flat[76:]
	}
	wrapped.WriteString(flat)
	encoded := wrapped.String()
	res := callMCP(t, session, "upload_document", map[string]any{"filename": "big.txt", "content_base64": encoded}, nil)
	if res.IsError {
		t.Fatalf("upload at the cap: %#v", res.Content)
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
