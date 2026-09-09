package ai

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/aiprovider"
)

// TestLiveTranslatedModel exercises a whole translated path -- Responses or
// Messages, whichever the model routes to -- against a real provider. It is
// skipped unless the variables below are set, because it spends somebody's
// tokens:
//
//	LIVE_AI_KEY=... LIVE_AI_SDK=opencode LIVE_AI_MODEL=gpt-5.6-luna \
//	  go test ./internal/ai/ -run Live -v
//
// LIVE_AI_MODEL is what picks the endpoint: gpt-5.6-luna for /responses,
// minimax-m3 for Anthropic's /messages, deepseek-v4-flash for the untranslated
// /chat/completions. LIVE_AI_BASE_URL is optional; it defaults to the SDK's own.
//
// It is the only way to check the parts a fake server cannot: that the
// translated request is one the provider actually accepts.
func TestLiveTranslatedModel(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("LIVE_AI_KEY"))
	sdk := strings.TrimSpace(os.Getenv("LIVE_AI_SDK"))
	model := strings.TrimSpace(os.Getenv("LIVE_AI_MODEL"))
	if key == "" || model == "" {
		t.Skip("set LIVE_AI_KEY and LIVE_AI_MODEL to run the live check")
	}
	if sdk == "" {
		sdk = aiprovider.SDKOpenCode
	}
	base := aiprovider.NormalizeBaseURL(sdk, os.Getenv("LIVE_AI_BASE_URL"))
	resetModelNotes()
	t.Cleanup(resetModelNotes)
	ctx := context.Background()

	t.Run("research", func(t *testing.T) {
		agent := NewSearchAgent(sdk, key, model, base, 120*time.Second, "en,de", "en", slog.Default())
		var read bool
		result, err := agent.Research(ctx, ResearchRequest{
			Messages: []ChatMessage{{Role: "user", Content: "How much did I pay for car insurance?"}},
			Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
				return hitsFor("doc1"), nil
			},
			Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
				read = true
				return []DocumentContent{{ID: "doc1", Title: "Allianz car insurance 2026", Text: "Annual premium: 412.90 EUR"}}, nil
			},
		}, func(ResearchEvent) {})
		if err != nil {
			t.Fatalf("Research: %v", err)
		}
		if !read {
			t.Error("the model never called read_documents")
		}
		if !strings.Contains(result.Reply, "412") {
			t.Errorf("reply lost the figure it was given: %q", result.Reply)
		}
		t.Logf("reply: %s", result.Reply)
	})

	t.Run("extract in json mode", func(t *testing.T) {
		client := NewOpenAIClient(sdk, key, model, base, "v1", "", 120*time.Second, slog.Default())
		metadata, err := client.ExtractMetadata(ctx,
			"Rechnung Nr. 4711\nAllianz SE\nDatum: 2026-03-14\nBetrag: 412,90 EUR", ExtractionCatalog{})
		if err != nil {
			t.Fatalf("ExtractMetadata: %v", err)
		}
		if metadata.DocumentDate != "2026-03-14" {
			t.Errorf("document date = %q", metadata.DocumentDate)
		}
		t.Logf("title=%q date=%q", metadata.Title, metadata.DocumentDate)
	})
}
