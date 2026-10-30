package ai

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"lemmary/backend/internal/report"
)

const spendChartArgs = `{"type":"bar","title":"Spend by year","labels":["2024","2025"],"series":[{"values":[120,80]}]}`

func spendChartID(t *testing.T) string {
	t.Helper()
	chart := report.Chart{Type: "bar", Title: "Spend by year", Labels: []string{"2024", "2025"}, Series: []report.Series{{Values: []float64{120, 80}}}}
	if err := chart.Normalize(); err != nil {
		t.Fatal(err)
	}
	return chart.ID()
}

func TestResearchEmbedsItsChartsAndLinksItsReport(t *testing.T) {
	t.Parallel()
	chartID := spendChartID(t)
	pdfArgs := fmt.Sprintf(`{"title":"Spend","content":"# Spend\n\n![Spend](chart:%s)"}`, chartID)
	doc := report.Document{Title: "Spend", Content: fmt.Sprintf("# Spend\n\n![Spend](chart:%s)", chartID)}
	pdfID := doc.ID()

	h, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{
			// A report cannot embed a chart nobody made.
			{name: "create_pdf", args: `{"title":"Early","content":"![x](chart:c0000000000)"}`},
			{name: "create_chart", args: spendChartArgs},
		}},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "create_pdf", args: pdfArgs}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: fmt.Sprintf("Spend fell.\n\n![Spend](chart:%s)\n\nReport: [Spend.pdf](pdf:%s). ![Ghost](chart:c1111111111) [Fake.pdf](pdf:p1111111111)", chartID, pdfID)},
	)

	var recorded []ThreadMessage
	var events []ResearchEvent
	result, err := agent.Research(context.Background(), ResearchRequest{
		Thread: []ThreadMessage{{Role: "user", Content: "Make me a PDF of my spending"}},
		Record: func(m ThreadMessage) { recorded = append(recorded, m) },
		Search: func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Read:   func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
	}, func(e ResearchEvent) { events = append(events, e) })
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	if early := toolMessageContent(t, h.requests[1], "create_pdf"); !strings.Contains(early, "make each chart with create_chart first") {
		t.Errorf("a report embedding an unknown chart should be refused: %s", early)
	}
	if !strings.Contains(result.Reply, "```chart\n{\"type\":\"bar\",\"title\":\"Spend by year\"") {
		t.Errorf("the chart should be embedded as a chart block: %q", result.Reply)
	}
	if !strings.Contains(result.Reply, "[Spend.pdf](pdf:"+pdfID+")") {
		t.Errorf("the report link should survive: %q", result.Reply)
	}
	if strings.Contains(result.Reply, "c1111111111") || strings.Contains(result.Reply, "(pdf:p1111111111)") || !strings.Contains(result.Reply, "Fake.pdf") {
		t.Errorf("invented charts should go and invented reports be unlinked: %q", result.Reply)
	}
	kinds := stepKinds(events)
	if !slices.Contains(kinds, "chart:done") || !slices.Contains(kinds, "pdf:done") {
		t.Errorf("steps = %v", kinds)
	}
	answer := fmt.Sprint(h.requests[len(h.requests)-1]["messages"])
	if !strings.Contains(answer, "(chart:<id>)") {
		t.Error("the answer instruction should say how to embed what the run made")
	}

	// The stored conversation alone is enough to find the report again.
	found := ThreadArtifacts(recorded)
	if _, ok := found.PDFs[pdfID]; !ok || len(found.PDFs) != 1 {
		t.Fatalf("reports read back = %v, want only %s", found.PDFs, pdfID)
	}
	if _, ok := found.Charts[chartID]; !ok {
		t.Fatalf("charts read back = %v", found.Charts)
	}
}

func TestAnAnswerWithNothingMadeIsNotToldAboutCharts(t *testing.T) {
	t.Parallel()
	if strings.Contains(researchAnswerInstruction(false, false), "chart:") {
		t.Fatal("no chart was made; the instruction should not invite one")
	}
}

func TestThreadArtifactsReadsALaterTurnsEmbedOfAnEarlierChart(t *testing.T) {
	t.Parallel()
	chartID := spendChartID(t)
	thread := []ThreadMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", Calls: []ToolCall{{ID: "1", Name: "create_chart", Arguments: spendChartArgs}, {ID: "2", Name: "create_chart", Arguments: `{"type":"pie"}`}}},
		{Role: "tool", CallID: "1", Content: "{}"},
	}
	found := ThreadArtifacts(thread)
	if len(found.Charts) != 1 {
		t.Fatalf("charts = %v, want the one valid chart", found.Charts)
	}
	if got := found.Embed("![again](chart:" + chartID + ")"); !strings.HasPrefix(strings.TrimSpace(got), "```chart") {
		t.Fatalf("embed = %q", got)
	}
}
