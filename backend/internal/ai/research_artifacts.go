package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"

	"lemmary/backend/internal/report"
)

// The two tools that make something besides the answer: a chart the answer
// embeds, and a PDF report the user downloads. Neither is stored anywhere but
// in the tool call that made it. An id is a hash of the call's content, so the
// stored conversation, read back, names every chart and report it ever made.

// Artifacts are the charts and reports a research conversation has made, by id.
type Artifacts struct {
	Charts map[string]report.Chart
	PDFs   map[string]report.Document
}

func newArtifacts() *Artifacts {
	return &Artifacts{Charts: map[string]report.Chart{}, PDFs: map[string]report.Document{}}
}

// ThreadArtifacts reads back what a stored conversation made, through the same
// checks its calls passed or failed when they were made.
func ThreadArtifacts(thread []ThreadMessage) *Artifacts {
	a := newArtifacts()
	for _, msg := range thread {
		if msg.Role != "assistant" {
			continue
		}
		calls := msg.Calls
		if len(calls) == 0 {
			for _, c := range parseDSMLToolCalls(msg.Content) {
				calls = append(calls, ToolCall(c))
			}
		}
		for _, call := range calls {
			switch call.Name {
			case "create_chart":
				_, _, _, _ = a.addChart(call.Arguments)
			case "create_pdf":
				_, _, _, _ = a.addPDF(call.Arguments)
			}
		}
	}
	return a
}

// addChart returns the chart a create_chart call describes, its id, and
// whether the conversation had not made it before.
func (a *Artifacts) addChart(arguments string) (report.Chart, string, bool, error) {
	var chart report.Chart
	if err := json.Unmarshal([]byte(arguments), &chart); err != nil {
		return chart, "", false, errors.New("invalid tool arguments")
	}
	if err := chart.Normalize(); err != nil {
		return chart, "", false, err
	}
	id := chart.ID()
	_, seen := a.Charts[id]
	a.Charts[id] = chart
	return chart, id, !seen, nil
}

func (a *Artifacts) addPDF(arguments string) (report.Document, string, bool, error) {
	var doc report.Document
	if err := json.Unmarshal([]byte(arguments), &doc); err != nil {
		return doc, "", false, errors.New("invalid tool arguments")
	}
	if err := doc.Normalize(); err != nil {
		return doc, "", false, err
	}
	var unknown []string
	for _, ref := range report.ChartRefs(doc.Content) {
		if _, ok := a.Charts[ref]; !ok {
			unknown = append(unknown, ref)
		}
	}
	if len(unknown) > 0 {
		return doc, "", false, fmt.Errorf("unknown chart ids %s: make each chart with create_chart first", strings.Join(unknown, ", "))
	}
	id := doc.ID()
	_, seen := a.PDFs[id]
	a.PDFs[id] = doc
	return doc, id, !seen, nil
}

var pdfLinkPattern = regexp.MustCompile(`\[([^\]\n]*)\]\(pdf:([a-z0-9]+)\)`)

// Embed puts each chart the answer places into it, as a fenced chart block the
// page draws, and unwraps links to reports this conversation never made -- the
// way validateCitations treats an invented document.
func (a *Artifacts) Embed(reply string) string {
	reply = report.ChartEmbed.ReplaceAllStringFunc(reply, func(match string) string {
		chart, ok := a.Charts[report.ChartEmbed.FindStringSubmatch(match)[2]]
		if !ok {
			return ""
		}
		encoded, _ := json.Marshal(chart)
		return "\n```chart\n" + string(encoded) + "\n```\n"
	})
	return pdfLinkPattern.ReplaceAllStringFunc(reply, func(match string) string {
		parts := pdfLinkPattern.FindStringSubmatch(match)
		if _, ok := a.PDFs[parts[2]]; ok {
			return match
		}
		return parts[1]
	})
}

func (a *Artifacts) empty() bool { return len(a.Charts) == 0 && len(a.PDFs) == 0 }

func runChartTool(state *researchState, callID, name, argumentsJSON string, emit func(ResearchEvent)) (toolExecResult, bool) {
	chart, id, fresh, err := state.artifacts.addChart(argumentsJSON)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, false
	}
	emit(ResearchEvent{Type: "step", Kind: "chart", Status: "done", Query: chart.Title})
	return artifactResult(callID, name, map[string]string{
		"id":    id,
		"embed": fmt.Sprintf("![%s](chart:%s)", linkText(chart.Title), id),
	}), fresh
}

func runPDFTool(state *researchState, callID, name, argumentsJSON string, emit func(ResearchEvent)) (toolExecResult, bool) {
	doc, id, fresh, err := state.artifacts.addPDF(argumentsJSON)
	if err != nil {
		return toolExecResult{ID: callID, Name: name, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, false
	}
	emit(ResearchEvent{Type: "step", Kind: "pdf", Status: "done", Query: doc.Title})
	return artifactResult(callID, name, map[string]string{
		"id":   id,
		"link": fmt.Sprintf("[%s.pdf](pdf:%s)", linkText(doc.Title), id),
	}), fresh
}

func artifactResult(callID, name string, payload map[string]string) toolExecResult {
	encoded, _ := json.Marshal(payload)
	return toolExecResult{ID: callID, Name: name, Content: string(encoded)}
}

// linkText keeps a title from closing the markdown it is put in.
func linkText(title string) string {
	return strings.NewReplacer("[", "(", "]", ")", "\n", " ").Replace(title)
}

func createChartTool() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name: "create_chart",
		Description: openai.String("Make a chart of figures you have gathered, to show in the answer or in a PDF report. " +
			"Use it when a distribution, a trend over time, or the shares of a total is easier to see than to read. " +
			"Returns an id and the markdown that embeds the chart; put that on a line of its own. " +
			"Chart only figures the tool results gave you."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{
					"type":        "string",
					"enum":        report.ChartTypes,
					"description": "bar to compare amounts or counts, line for a trend over time, pie for the shares of one total.",
				},
				"title": map[string]any{"type": "string", "description": "What the chart shows, with its unit, in the user's language."},
				"labels": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": fmt.Sprintf("The categories or periods along the axis, or the pie's slices; at most %d.", report.MaxPoints),
				},
				"series": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":   map[string]any{"type": "string", "description": "Required when there are several series."},
							"values": map[string]any{"type": "array", "items": map[string]any{"type": "number"}, "description": "One number per label, in label order."},
						},
						"required": []string{"values"},
					},
					"description": fmt.Sprintf("At most %d series; a pie takes exactly one, of at most %d non-negative values.", report.MaxSeries, report.MaxSeries),
				},
			},
			"required": []string{"type", "title", "labels", "series"},
		},
	})
}

func createPDFTool() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name: "create_pdf",
		Description: openai.String("Write a PDF report the user can download. Use it only when the user asks for a report, a document or a PDF, once the evidence is gathered. " +
			"content is the whole report in markdown: headings, paragraphs, lists, tables, and charts embedded with the markdown create_chart returned. " +
			"Returns the markdown link to put in the answer."),
		Parameters: shared.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{"type": "string", "description": "The report's title, in the user's language."},
				"content": map[string]any{
					"type":        "string",
					"description": "The report in markdown. Cite documents as [title](/document/<id>) the way the answer does; they print as their title.",
				},
			},
			"required": []string{"title", "content"},
		},
	})
}
