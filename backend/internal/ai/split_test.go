package ai

import (
	"strings"
	"testing"
)

func TestBuildSplitUserMessageLabelsEveryPage(t *testing.T) {
	t.Parallel()

	message := buildSplitUserMessage([]PageText{
		{Page: 1, Text: "Invoice INV-1001"},
		{Page: 2, Text: "Continued items"},
		{Page: 3, Text: ""},
	})

	if !strings.Contains(message, "The file has 3 pages.") {
		t.Fatalf("expected the page count header, got %q", message)
	}
	for _, want := range []string{"--- PAGE 1 ---", "--- PAGE 2 ---", "--- PAGE 3 ---"} {
		if !strings.Contains(message, want) {
			t.Fatalf("missing %q in %q", want, message)
		}
	}
	if !strings.Contains(message, "Invoice INV-1001") {
		t.Fatalf("page text dropped: %q", message)
	}
	if !strings.Contains(message, "(no text on this page)") {
		t.Fatalf("expected an explicit marker for the empty page, got %q", message)
	}
}

func TestBuildSplitUserMessagePerPageBudgetShrinksWithPageCount(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", splitPromptTotalChars)

	twoPages := buildSplitUserMessage([]PageText{
		{Page: 1, Text: long},
		{Page: 2, Text: long},
	})
	tenPages := make([]PageText, 10)
	for i := range tenPages {
		tenPages[i] = PageText{Page: i + 1, Text: long}
	}
	ten := buildSplitUserMessage(tenPages)

	// Both stay near the total budget rather than growing with the page count.
	if len(twoPages) > splitPromptTotalChars*2 {
		t.Fatalf("two-page prompt is %d chars, expected it near the %d budget", len(twoPages), splitPromptTotalChars)
	}
	if len(ten) > splitPromptTotalChars*2 {
		t.Fatalf("ten-page prompt is %d chars, expected it near the %d budget", len(ten), splitPromptTotalChars)
	}
	if strings.Count(ten, "--- PAGE ") != 10 {
		t.Fatalf("expected all 10 pages represented, got %d", strings.Count(ten, "--- PAGE "))
	}
}

func TestBuildSplitUserMessageKeepsALongFileReadable(t *testing.T) {
	t.Parallel()

	// With 200 pages the fair share falls under the floor, so the floor decides.
	pages := make([]PageText, 200)
	for i := range pages {
		pages[i] = PageText{Page: i + 1, Text: strings.Repeat("b", 5000)}
	}
	message := buildSplitUserMessage(pages)

	if strings.Count(message, "--- PAGE ") != 200 {
		t.Fatalf("expected all 200 pages represented, got %d", strings.Count(message, "--- PAGE "))
	}
	if len(message) > 200*(splitPromptMinPageChars+64) {
		t.Fatalf("prompt grew past the per-page floor: %d chars", len(message))
	}
}
