package ai

import "testing"

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
