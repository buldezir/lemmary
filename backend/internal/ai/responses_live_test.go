package ai

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveResponsesOnlyModel exercises the whole Responses path against a real
// provider. It is skipped unless the three variables below are set, because it
// spends somebody's tokens:
//
//	LIVE_AI_KEY=... LIVE_AI_BASE_URL=https://opencode.ai/zen/go/v1 \
//	LIVE_AI_MODEL=gpt-5.6-luna go test ./internal/ai/ -run Live -v
//
// It is the only way to check the parts a fake server cannot: that the
// translated request is one the provider actually accepts.
func TestLiveResponsesOnlyModel(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("LIVE_AI_KEY"))
	base := strings.TrimSpace(os.Getenv("LIVE_AI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("LIVE_AI_MODEL"))
	if key == "" || base == "" || model == "" {
		t.Skip("set LIVE_AI_KEY, LIVE_AI_BASE_URL and LIVE_AI_MODEL to run the live check")
	}
	resetResponsesAPI()
	t.Cleanup(resetResponsesAPI)
	ctx := context.Background()

	t.Run("research", func(t *testing.T) {
		agent := NewSearchAgent("openai", key, model, base, 120*time.Second, "en,de", "en", slog.Default())
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
		client := NewOpenAIClient("openai", key, model, base, "v1", "", 120*time.Second, slog.Default())
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
