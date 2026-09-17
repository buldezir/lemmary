package appapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/fulltext"
)

// EnvMCPEnabled switches the MCP endpoint at /api/mcp. Unset means on: the
// endpoint answers only to a bearer token and costs nothing until an agent
// calls it, so there is nothing to protect an install from by default. Set it
// to 0 to take the route away.
const EnvMCPEnabled = "MCP_ENABLED"

const (
	mcpMaxBodyBytes = 1 << 20
	// mcpTokenTTL is how long a token minted for an agent lasts. Ten years,
	// like the paperless one: an agent config is written once and never
	// refreshes. Changing the password invalidates it, as with any token.
	mcpTokenTTL = 10 * 365 * 24 * time.Hour
	// mcpMaxReadIDs bounds one read_documents call. The in-app agent is held
	// to a handful by its model; an outside agent is held to it here.
	mcpMaxReadIDs = 10
)

// RegisterMCP mounts a read-only Model Context Protocol server over the same
// retrieval closures Deep Search uses, so an outside agent sees exactly what
// the in-app agent sees for that token's user. Stateless: every request builds
// its own server bound to the caller, which is what makes per-user scoping
// fall out of the existing auth binder instead of session bookkeeping. Every
// token, superuser or not, is scoped to one users account.
func RegisterMCP(app core.App, rt *config.Runtime, idx *fulltext.Index) {
	enabled := mcpEnabledFromEnv(app.Logger())
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Priority: 45,
		Func: func(e *core.ServeEvent) error {
			// Answered either way: an absent route would fall through to the
			// paperless GET /api/ root and look like a yes to the Account page.
			e.Router.GET("/api/app/mcp", bindAuth(func(e *core.RequestEvent) error {
				return writeJSON(e, http.StatusOK, map[string]any{"enabled": enabled, "path": "/api/mcp"})
			}))
			if !enabled {
				return e.Next()
			}
			e.Router.POST("/api/mcp", bindAuth(handleMCP(app, rt, idx))).
				Bind(apis.BodyLimit(mcpMaxBodyBytes))
			e.Router.POST("/api/app/mcp/token", bindAuth(handleMCPToken(app)))
			return e.Next()
		},
	})
}

func mcpEnabledFromEnv(log *slog.Logger) bool {
	raw := strings.TrimSpace(os.Getenv(EnvMCPEnabled))
	if raw == "" {
		return true
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	// Refused rather than guessed, and refused towards off: an operator who
	// wrote something here meant to change the default.
	log.Error("MCP endpoint disabled: not a boolean; use 1/true/yes/on or 0/false/no/off",
		"env", EnvMCPEnabled, "value", raw)
	return false
}

// handleMCPToken mints a long-lived token for the caller's own users account,
// the thing an agent config needs and a browser session cannot give it.
func handleMCPToken(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		userID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		user, err := app.FindRecordById("users", userID)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to load the account.")
		}
		token, err := user.NewStaticAuthToken(mcpTokenTTL)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to create the token.")
		}
		return writeJSON(e, http.StatusOK, map[string]any{
			"token":   token,
			"expires": time.Now().Add(mcpTokenTTL).UTC().Format(time.RFC3339),
		})
	}
}

func handleMCP(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		// Always one account's view. A superuser token resolves to its paired
		// users record rather than to every account: the admin is also an
		// ordinary user here, and an agent holding its token gets that
		// user's archive, not everyone's.
		userID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		// No distillation: a read is excerpted text, never a billed summary.
		tools, err := buildAgentTools(app, rt, idx, userID, false)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to prepare the document tools.")
		}
		server := newMCPServer(tools, newMCPDocs(app, userID))
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
			&mcp.StreamableHTTPOptions{
				Stateless:    true,
				JSONResponse: true,
				// The app usually sits behind a reverse proxy on a loopback
				// listener with a public Host header, which the SDK's default
				// check would refuse; the route is bearer-protected already.
				DisableLocalhostProtection: true,
			})
		handler.ServeHTTP(e.Response, e.Request)
		return nil
	}
}

type mcpSearchArgs struct {
	Query         string   `json:"query" jsonschema:"What to look for, as keywords or a short phrase. Matched against titles, summaries and OCR text; not every word has to occur, so describe the thing rather than guessing its exact wording."`
	DateFrom      string   `json:"date_from,omitempty" jsonschema:"Inclusive lower bound for document_date (YYYY-MM-DD)."`
	DateTo        string   `json:"date_to,omitempty" jsonschema:"Inclusive upper bound for document_date (YYYY-MM-DD)."`
	DocumentType  string   `json:"document_type,omitempty" jsonschema:"Document type name filter (substring match)."`
	Correspondent string   `json:"correspondent,omitempty" jsonschema:"Correspondent name filter (substring match)."`
	Tags          []string `json:"tags,omitempty" jsonschema:"Exact tag names from list_taxonomy; documents with any of them match."`
}

type mcpSearchResult struct {
	Hits []ai.DocumentHit `json:"hits"`
}

type mcpReadArgs struct {
	IDs   []string `json:"ids" jsonschema:"Document ids from earlier search_documents results, at most 10 per call."`
	Focus string   `json:"focus,omitempty" jsonschema:"What you are looking for in these documents. A long document comes back as the passages about this, with … marking the gaps, instead of only its beginning."`
}

type mcpReadResult struct {
	Documents []ai.DocumentContent `json:"documents"`
}

type mcpCountArgs struct {
	Query         string   `json:"query,omitempty" jsonschema:"Keywords every counted document must contain."`
	DateFrom      string   `json:"date_from,omitempty" jsonschema:"Inclusive lower bound on document_date, YYYY-MM-DD."`
	DateTo        string   `json:"date_to,omitempty" jsonschema:"Inclusive upper bound on document_date, YYYY-MM-DD."`
	DocumentType  string   `json:"document_type,omitempty" jsonschema:"Document type name filter."`
	Correspondent string   `json:"correspondent,omitempty" jsonschema:"Correspondent name filter."`
	Tags          []string `json:"tags,omitempty" jsonschema:"Exact tag names; documents with any of them match."`
	GroupBy       string   `json:"group_by,omitempty" jsonschema:"Break the count down by one of: document_type, correspondent, year, month, tag."`
}

func newMCPServer(tools agentTools, docs mcpDocs) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "lemmary", Version: "1"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "search_documents",
		Description: "Search the archive by meaning and by keywords (hybrid full-text and vector index), with optional filters. Only documents that have finished processing (status completed) are searched. " +
			"Returns matching documents with 1-3 verbatim passages from each. For a plain filtered listing use list_documents.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args mcpSearchArgs) (*mcp.CallToolResult, mcpSearchResult, error) {
		hits, err := tools.search(ctx, ai.SearchDocumentsArgs{
			Query:         args.Query,
			DateFrom:      args.DateFrom,
			DateTo:        args.DateTo,
			DocumentType:  args.DocumentType,
			Correspondent: args.Correspondent,
			Tags:          args.Tags,
		})
		if err != nil {
			return nil, mcpSearchResult{}, err
		}
		if hits == nil {
			hits = []ai.DocumentHit{}
		}
		return nil, mcpSearchResult{Hits: hits}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "read_documents",
		Description: "Read the parts of up to 10 documents that matter for a focus: a long document comes back as " +
			"the passages ranked against the focus by the same index search_documents uses, with … marking the gaps. " +
			"For the whole text of one document use get_document.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args mcpReadArgs) (*mcp.CallToolResult, mcpReadResult, error) {
		if len(args.IDs) == 0 {
			return nil, mcpReadResult{}, fmt.Errorf("ids is required")
		}
		if len(args.IDs) > mcpMaxReadIDs {
			return nil, mcpReadResult{}, fmt.Errorf("at most %d ids per call", mcpMaxReadIDs)
		}
		docs, err := tools.read(ctx, ai.ReadRequest{IDs: args.IDs, Focus: args.Focus})
		if err != nil {
			return nil, mcpReadResult{}, err
		}
		if docs == nil {
			docs = []ai.DocumentContent{}
		}
		return nil, mcpReadResult{Documents: docs}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "count_documents",
		Description: "Count the documents matching filters, optionally grouped. " +
			"Use this for how-many and distribution questions instead of counting search results: a search result is a capped page, not the archive.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args mcpCountArgs) (*mcp.CallToolResult, ai.CountResult, error) {
		if tools.count == nil {
			return nil, ai.CountResult{}, fmt.Errorf("counting is unavailable")
		}
		result, err := tools.count(ctx, ai.CountArgs{
			Query:         args.Query,
			DateFrom:      args.DateFrom,
			DateTo:        args.DateTo,
			DocumentType:  args.DocumentType,
			Correspondent: args.Correspondent,
			Tags:          args.Tags,
			GroupBy:       args.GroupBy,
		})
		if err != nil {
			return nil, ai.CountResult{}, err
		}
		return nil, result, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_documents",
		Description: "List documents by metadata, newest first by default, with paging. No text search: " +
			"filter by date range, document type, correspondent, tags and processing status. Returns metadata only; get_document returns the text.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args mcpListArgs) (*mcp.CallToolResult, mcpListResult, error) {
		result, err := docs.list(ctx, args)
		if err != nil {
			return nil, mcpListResult{}, err
		}
		return nil, result, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_document",
		Description: "One document's metadata and its full extracted text, unranked and unabridged. " +
			"Text is paged by offset and max_chars (at most 200000); text_chars is the whole length and truncated means more follows this page.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args mcpGetArgs) (*mcp.CallToolResult, mcpGetResult, error) {
		result, err := docs.get(ctx, args)
		if err != nil {
			return nil, mcpGetResult{}, err
		}
		return nil, result, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_taxonomy",
		Description: "The tag, document type and correspondent names in the archive, for the filters of the other tools. " +
			"Each list is cut at 5000 names, and truncated says when that happened.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, mcpTaxonomyResult, error) {
		result, err := docs.taxonomy(ctx)
		if err != nil {
			return nil, mcpTaxonomyResult{}, err
		}
		for _, names := range []*[]string{&result.Tags, &result.DocumentTypes, &result.Correspondents} {
			if *names == nil {
				*names = []string{}
			}
		}
		return nil, result, nil
	})

	return server
}
