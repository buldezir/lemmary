package ai

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// A model name means nothing on its own. An instance can bind several providers
// at once, so what one gateway does with "gpt-5.6-luna" must not follow the
// name onto another.
func TestModelNotesAreScopedToTheirEndpoint(t *testing.T) {
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	const model = "gpt-5.6-luna"
	zen := "https://opencode.ai/zen/go/v1"
	openai := "https://api.openai.com/v1"

	rememberResponsesAPI(zen, model)
	rememberNoReasoningEffort(zen, model)

	if !needsResponsesAPI(zen, model) || !needsNoReasoningEffort(zen, model) {
		t.Fatal("the endpoint that taught us this does not remember it")
	}
	if needsResponsesAPI(openai, model) {
		t.Fatal("one gateway's routing leaked onto another")
	}
	if needsNoReasoningEffort(openai, model) {
		t.Fatal("one gateway's reasoning_effort verdict leaked onto another")
	}

	// A trailing slash and a different case are the same endpoint.
	if !needsResponsesAPI("https://OpenCode.ai/zen/go/v1/", "GPT-5.6-Luna") {
		t.Fatal("the same endpoint was not recognised through normalisation")
	}
	// An unnamed model is not a note.
	rememberResponsesAPI(zen, "  ")
	if needsResponsesAPI(zen, "") {
		t.Fatal("an empty model name was stored as a note")
	}
}

// The same model behind two providers must be discovered separately, all the
// way through CompleteChat rather than only in the note store.
func TestDiscoveryDoesNotLeakBetweenProviders(t *testing.T) {
	resetModelNotes()
	t.Cleanup(resetModelNotes)
	const model = "shared-model-name"

	// One provider serves this model only on /responses.
	responsesOnly := &responsesHarness{chatStatus: http.StatusNotFound, respTurns: []scriptedTurn{{content: "from responses"}}}
	responsesBase := newResponsesHarness(t, responsesOnly)
	// The other serves it perfectly well on /chat/completions.
	ordinary := &responsesHarness{}
	ordinaryBase := newResponsesHarness(t, ordinary)

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}
	first := NewOpenAIClient("openai", "k", model, responsesBase, "v1", "", 5*time.Second, slog.Default())
	if _, err := CompleteChat(context.Background(), first.client, slog.Default(), "openai", responsesBase, params); err != nil {
		t.Fatalf("responses-only provider: %v", err)
	}

	second := NewOpenAIClient("openai", "k", model, ordinaryBase, "v1", "", 5*time.Second, slog.Default())
	resp, err := CompleteChat(context.Background(), second.client, slog.Default(), "openai", ordinaryBase, params)
	if err != nil {
		t.Fatalf("ordinary provider: %v", err)
	}
	if resp.Choices[0].Message.Content != "served by chat completions" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
	if _, r := ordinary.counts(); r != 0 {
		t.Fatalf("the other provider's discovery sent %d requests to this one's /responses", r)
	}
}
