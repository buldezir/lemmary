package ai

import (
	"strings"
	"testing"
)

func TestFormatAvailableTagsPrompt(t *testing.T) {
	empty := formatAvailableTagsPrompt(nil)
	if !strings.Contains(empty, "none are defined") {
		t.Fatalf("expected empty-tags guidance, got %q", empty)
	}

	withTags := formatAvailableTagsPrompt([]string{" invoice ", "plumbing", "Invoice", ""})
	if !strings.Contains(withTags, "invoice") || !strings.Contains(withTags, "plumbing") {
		t.Fatalf("expected tag names in prompt, got %q", withTags)
	}
	if strings.Count(strings.ToLower(withTags), "invoice") != 1 {
		t.Fatalf("expected deduped invoice tag, got %q", withTags)
	}
	if !strings.Contains(withTags, "tags filter") {
		t.Fatalf("expected tags filter guidance, got %q", withTags)
	}
}

func TestBuildSearchSystemPromptIncludesTags(t *testing.T) {
	prompt := buildSearchSystemPrompt("en,de", "en", []string{"invoice", "tax"}, false)
	if !strings.Contains(prompt, "invoice") || !strings.Contains(prompt, "tax") {
		t.Fatalf("expected available tags in system prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "tags filter") {
		t.Fatalf("expected tags filter instruction, got %q", prompt)
	}
	if !strings.Contains(prompt, "en,de") {
		t.Fatalf("expected archive languages in system prompt, got %q", prompt)
	}
	// Search mode is one round now; the deep-mode knob is gone, and a question
	// that needs more belongs in Research mode.
	if strings.Contains(strings.ToLower(prompt), "deep search mode") {
		t.Fatalf("search prompt still advertises deep mode: %q", prompt)
	}
	if !strings.Contains(prompt, "one round of tool calls") {
		t.Fatalf("expected single-round instruction, got %q", prompt)
	}
	if !strings.Contains(prompt, "Research mode") {
		t.Fatalf("expected a pointer to Research mode, got %q", prompt)
	}
}

func TestSearchDocumentsToolsDoesNotAdvertiseAResultCap(t *testing.T) {
	t.Parallel()
	tools := searchDocumentsTools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	props, _ := tools[0].Function.Parameters["properties"].(map[string]any)
	if _, ok := props["limit"]; ok {
		t.Fatal("search_documents still advertises limit; models then cap themselves at 10-20")
	}
}

func TestFormatLanguagePromptFallsBackToResultLanguage(t *testing.T) {
	withList := formatLanguagePrompt("de,uk", "en", false)
	if !strings.Contains(withList, "de,uk") {
		t.Fatalf("expected the configured list, got %q", withList)
	}

	withoutList := formatLanguagePrompt("", "en", false)
	if !strings.Contains(withoutList, "en") {
		t.Fatalf("expected the result language as fallback, got %q", withoutList)
	}
	if strings.Contains(withoutList, "deep-search") {
		t.Fatalf("prompt still names the removed deep-search knob: %q", withoutList)
	}
}

func TestDecodeSearchArgsCoercesScalarKinds(t *testing.T) {
	// Models (and the DSML fallback) routinely emit the wrong JSON scalar
	// kinds; the whole tool call used to be dropped as invalid.
	args, err := decodeSearchArgs(`{"query": 2023, "tags": ["invoice", 7], "limit": "5"}`)
	if err != nil {
		t.Fatalf("decodeSearchArgs: %v", err)
	}
	if args.Query != "2023" {
		t.Fatalf("query = %q, want \"2023\"", args.Query)
	}
	if len(args.Tags) != 2 || args.Tags[0] != "invoice" || args.Tags[1] != "7" {
		t.Fatalf("tags = %v", args.Tags)
	}
	if args.Limit != 5 {
		t.Fatalf("limit = %d, want 5", args.Limit)
	}

	args, err = decodeSearchArgs(`{"query": "plain", "limit": 3}`)
	if err != nil || args.Query != "plain" || args.Limit != 3 {
		t.Fatalf("well-formed args mishandled: %+v err=%v", args, err)
	}

	if _, err := decodeSearchArgs(`not json`); err == nil {
		t.Fatal("expected error for non-JSON arguments")
	}
}

func TestFormatLanguagePromptWithDenseRetrievalStopsPerLanguageSearches(t *testing.T) {
	dense := formatLanguagePrompt("de,uk", "en", true)
	if !strings.Contains(dense, "Do not repeat a search translated into another language") {
		t.Fatalf("dense prompt still invites per-language searches: %q", dense)
	}
	if strings.Contains(dense, "once per language") {
		t.Fatalf("dense prompt kept the keyword-only instruction: %q", dense)
	}
	if !strings.Contains(dense, "de,uk") {
		t.Fatalf("dense prompt should still name the archive languages for exact terms: %q", dense)
	}

	keyword := formatLanguagePrompt("de,uk", "en", false)
	if !strings.Contains(keyword, "once per language") {
		t.Fatalf("keyword-only prompt lost the per-language instruction: %q", keyword)
	}
}
