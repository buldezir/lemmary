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
	prompt := buildSearchSystemPrompt("en,de", "en", SearchModeDeep, []string{"invoice", "tax"})
	if !strings.Contains(prompt, "invoice") || !strings.Contains(prompt, "tax") {
		t.Fatalf("expected available tags in system prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "tags filter") {
		t.Fatalf("expected tags filter instruction, got %q", prompt)
	}
	if !strings.Contains(prompt, "deep search mode") {
		t.Fatalf("expected deep mode instruction, got %q", prompt)
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
