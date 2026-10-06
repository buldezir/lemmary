package appapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/reprocess"
	"lemmary/backend/internal/worker"
)

// mcpMaxBatchIDs bounds one reprocess_documents or delete_documents call.
const mcpMaxBatchIDs = 50

type mcpUpdateArgs struct {
	ID            string    `json:"id" jsonschema:"Document id."`
	Title         *string   `json:"title,omitempty" jsonschema:"New title. Left out keeps the current one."`
	Summary       *string   `json:"summary,omitempty" jsonschema:"New summary. Left out keeps the current one."`
	DocumentDate  *string   `json:"document_date,omitempty" jsonschema:"YYYY-MM-DD; empty clears it."`
	DocumentType  *string   `json:"document_type,omitempty" jsonschema:"Document type name, created if new; empty clears it."`
	Correspondent *string   `json:"correspondent,omitempty" jsonschema:"Correspondent name, created if new; empty clears it."`
	Tags          *[]string `json:"tags,omitempty" jsonschema:"The whole tag list as exact names of existing tags. Replaces the current tags; an empty list removes them all."`
	MarkReviewed  bool      `json:"mark_reviewed,omitempty" jsonschema:"Move a needs_review document to completed."`
}

type mcpIDsArgs struct {
	IDs []string `json:"ids" jsonschema:"Document ids, at most 50."`
}

type mcpReprocessArgs struct {
	IDs  []string `json:"ids" jsonschema:"Document ids, at most 50."`
	Mode string   `json:"mode,omitempty" jsonschema:"auto (default): extraction only where OCR text exists, else everything; full: OCR and extraction; extraction: extraction only."`
}

type mcpUploadArgs struct {
	Filename      string `json:"filename" jsonschema:"File name with its extension. PDF, JPEG, PNG, WebP or plain text."`
	ContentBase64 string `json:"content_base64" jsonschema:"The file, base64-encoded (wrapped, unpadded or a data: URL are fine); at most 20 MB decoded."`
}

type mcpDeleteResult struct {
	Deleted  []string `json:"deleted"`
	NotFound []string `json:"not_found"`
}

type mcpTagArgs struct {
	Name string `json:"name" jsonschema:"Exact tag name."`
}

type mcpRenameTagArgs struct {
	Name    string `json:"name" jsonschema:"Exact current tag name."`
	NewName string `json:"new_name" jsonschema:"The name to give it."`
}

type mcpTagResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created bool   `json:"created,omitempty"`
}

// addMCPWriteTools offers only the tools an admin switched on in Settings →
// MCP, so a switched-off tool is absent from tools/list rather than refused.
// Every write is to records the caller owns: a share is a read-only grant.
func addMCPWriteTools(server *mcp.Server, app core.App, userID string, capabilities []string) {
	if slices.Contains(capabilities, config.MCPEdit) {
		addMCPTool(server, "update_document",
			"Change one document's title, summary, date, document type, correspondent or tags, or mark it reviewed. "+
				"Fields left out keep their value; the change is recorded as made by hand.",
			func(args mcpUpdateArgs) (mcpDocument, error) { return updateMCPDocument(app, userID, args) })
	}
	if slices.Contains(capabilities, config.MCPReprocess) {
		addMCPTool(server, "reprocess_documents",
			"Queue documents for OCR and extraction again. Costs AI usage, and extraction overwrites the document's metadata, "+
				"hand edits included. Documents already pending or processing are skipped.",
			func(args mcpReprocessArgs) (reprocess.Result, error) {
				return reprocessMCPDocuments(app, userID, args)
			})
	}
	if slices.Contains(capabilities, config.MCPUpload) {
		addMCPTool(server, "upload_document",
			"Add a file to the archive. It is queued for OCR and extraction, which cost AI usage and set its title and metadata; "+
				"a file already in the archive is refused with the id of the copy.",
			func(args mcpUploadArgs) (mcpDocument, error) { return uploadMCPDocument(app, userID, args) })
	}
	if slices.Contains(capabilities, config.MCPDelete) {
		addMCPTool(server, "delete_documents",
			"Delete documents permanently, with their files: all of them or, on an error, none. There is no trash and no undo.",
			func(args mcpIDsArgs) (mcpDeleteResult, error) { return deleteMCPDocuments(app, userID, args.IDs) })
	}
	if slices.Contains(capabilities, config.MCPTags) {
		addMCPTagTools(server, app, userID)
	}
}

func addMCPTagTools(server *mcp.Server, app core.App, userID string) {
	addMCPTool(server, "create_tag", "Create a tag, or return the existing one of that name.",
		func(args mcpTagArgs) (mcpTagResult, error) {
			id, created, err := worker.EnsureTag(app, userID, args.Name)
			if err != nil {
				return mcpTagResult{}, err
			}
			if id == "" {
				return mcpTagResult{}, fmt.Errorf("name is required")
			}
			return mcpTagResult{ID: id, Name: strings.TrimSpace(args.Name), Created: created}, nil
		})
	addMCPTool(server, "rename_tag", "Rename a tag on every document that has it.",
		func(args mcpRenameTagArgs) (mcpTagResult, error) {
			tag, err := findOwnedMCPTag(app, userID, args.Name)
			if err != nil {
				return mcpTagResult{}, err
			}
			name := strings.TrimSpace(args.NewName)
			if name == "" {
				return mcpTagResult{}, fmt.Errorf("new_name is required")
			}
			tag.Set("name", name)
			if err := app.Save(tag); err != nil {
				return mcpTagResult{}, err
			}
			return mcpTagResult{ID: tag.Id, Name: name}, nil
		})
	addMCPTool(server, "delete_tag", "Delete a tag and take it off every document. The documents stay.",
		func(args mcpTagArgs) (mcpTagResult, error) {
			tag, err := findOwnedMCPTag(app, userID, args.Name)
			if err != nil {
				return mcpTagResult{}, err
			}
			if err := app.Delete(tag); err != nil {
				return mcpTagResult{}, err
			}
			return mcpTagResult{ID: tag.Id, Name: tag.GetString("name")}, nil
		})
}

func addMCPTool[In, Out any](server *mcp.Server, name, description string, run func(In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description},
		func(_ context.Context, _ *mcp.CallToolRequest, args In) (*mcp.CallToolResult, Out, error) {
			out, err := run(args)
			return nil, out, err
		})
}

func updateMCPDocument(app core.App, userID string, args mcpUpdateArgs) (mcpDocument, error) {
	record, err := findOwnedMCPDocument(app, userID, args.ID)
	if err != nil {
		return mcpDocument{}, err
	}
	if err := applyMCPUpdate(app, userID, record, args); err != nil {
		return mcpDocument{}, err
	}
	fields := map[string]any{}
	if args.DocumentType != nil {
		fields[models.DocumentTypeFieldID] = *args.DocumentType
	}
	if args.Correspondent != nil {
		fields[models.CorrespondentFieldID] = *args.Correspondent
	}
	writes, err := documentFieldWrites(app, record, fields)
	if err != nil {
		return mcpDocument{}, err
	}
	record.Set("metadata_source", models.MetadataSourceUser)
	err = app.RunInTransaction(func(txApp core.App) error {
		if err := models.SaveFieldValues(txApp, record, writes); err != nil {
			return err
		}
		return txApp.Save(record)
	})
	if err != nil {
		return mcpDocument{}, err
	}
	expandMCPDocuments(app, []*core.Record{record})
	return mcpDocumentOf(record), nil
}

// applyMCPUpdate sets the document's own columns; the option fields are
// custom field values, saved beside it.
func applyMCPUpdate(app core.App, userID string, record *core.Record, args mcpUpdateArgs) error {
	if args.Title != nil {
		record.Set("title", strings.TrimSpace(*args.Title))
	}
	if args.Summary != nil {
		record.Set("summary", strings.TrimSpace(*args.Summary))
	}
	if args.DocumentDate != nil {
		date := strings.TrimSpace(*args.DocumentDate)
		if _, err := time.Parse(time.DateOnly, date); date != "" && err != nil {
			return fmt.Errorf("document_date must be YYYY-MM-DD")
		}
		record.Set("document_date", date)
	}
	if args.Tags != nil {
		ids := make([]string, 0, len(*args.Tags))
		for _, name := range *args.Tags {
			tag, err := findOwnedMCPTag(app, userID, name)
			if err != nil {
				return err
			}
			ids = append(ids, tag.Id)
		}
		record.Set("tags", ids)
	}
	if args.MarkReviewed && record.GetString("processing_status") == models.DocStatusNeedsReview {
		record.Set("processing_status", models.DocStatusCompleted)
	}
	return nil
}

func reprocessMCPDocuments(app core.App, userID string, args mcpReprocessArgs) (reprocess.Result, error) {
	if err := checkMCPBatch(args.IDs); err != nil {
		return reprocess.Result{}, err
	}
	mode, err := reprocess.ParseMode(args.Mode)
	if err != nil {
		return reprocess.Result{}, err
	}
	return reprocess.RunBatch(app, reprocess.Request{OwnerUserID: userID, DocumentIDs: args.IDs, Mode: mode})
}

// uploadMCPDocument stores what the upload page stores; the documents hooks
// then check type, size, allowance and duplicates and queue the processing.
func uploadMCPDocument(app core.App, userID string, args mcpUploadArgs) (mcpDocument, error) {
	name := strings.TrimSpace(args.Filename)
	if name == "" {
		return mcpDocument{}, fmt.Errorf("filename is required")
	}
	data, err := decodeMCPFile(args.ContentBase64)
	if err != nil {
		return mcpDocument{}, err
	}
	file, err := filesystem.NewFileFromBytes(data, name)
	if err != nil {
		return mcpDocument{}, err
	}
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return mcpDocument{}, err
	}
	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("file", file)
	record.Set("processing_status", models.DocStatusPending)
	if err := app.Save(record); err != nil {
		return mcpDocument{}, err
	}
	return mcpDocumentOf(record), nil
}

// decodeMCPFile takes base64 the way tools write it: wrapped at any width,
// padded or not, or as a data: URL.
func decodeMCPFile(content string) ([]byte, error) {
	content = strings.Join(strings.Fields(content), "")
	if strings.HasPrefix(content, "data:") {
		_, content, _ = strings.Cut(content, ",")
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(content, "="))
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("content_base64 must be the file, base64-encoded")
	}
	if len(data) > mcpMaxUploadBytes {
		return nil, fmt.Errorf("the file is %d bytes; at most %d", len(data), mcpMaxUploadBytes)
	}
	return data, nil
}

// deleteMCPDocuments deletes all of them or none, so a failure part-way
// leaves nothing the caller was not told about.
func deleteMCPDocuments(app core.App, userID string, ids []string) (mcpDeleteResult, error) {
	if err := checkMCPBatch(ids); err != nil {
		return mcpDeleteResult{}, err
	}
	out := mcpDeleteResult{Deleted: []string{}, NotFound: []string{}}
	err := app.RunInTransaction(func(txApp core.App) error {
		for _, id := range ids {
			record, err := findOwnedMCPDocument(txApp, userID, id)
			if errors.Is(err, errNoOwnedDocument) {
				out.NotFound = append(out.NotFound, id)
				continue
			}
			if err != nil {
				return err
			}
			if err := txApp.Delete(record); err != nil {
				return err
			}
			out.Deleted = append(out.Deleted, record.Id)
		}
		return nil
	})
	if err != nil {
		return mcpDeleteResult{}, err
	}
	return out, nil
}

func checkMCPBatch(ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("ids is required")
	}
	if len(ids) > mcpMaxBatchIDs {
		return fmt.Errorf("at most %d ids per call", mcpMaxBatchIDs)
	}
	return nil
}

var errNoOwnedDocument = errors.New("no such document of yours")

// findOwnedMCPDocument tells a missing or someone else's document
// (errNoOwnedDocument) from a lookup that failed.
func findOwnedMCPDocument(app core.App, userID, id string) (*core.Record, error) {
	id = strings.TrimSpace(id)
	record, err := app.FindRecordById("documents", id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && record.GetString("user") != userID) {
		return nil, fmt.Errorf("%s: %w", id, errNoOwnedDocument)
	}
	return record, err
}

func findOwnedMCPTag(app core.App, userID, name string) (*core.Record, error) {
	name = strings.TrimSpace(name)
	tags, err := findTagsByNameFilter(app, "name = {:name}", name, []string{userID})
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("no tag named %q; list_taxonomy has the names", name)
	}
	return tags[0], nil
}
