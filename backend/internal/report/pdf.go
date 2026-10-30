package report

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"codeberg.org/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

const (
	margin     = 18.0
	bodySize   = 10.5
	lineHeight = 5.2
	sans       = "go"
	mono       = "gomono"
)

type rgb struct{ r, g, b int }

var (
	inkColor   = rgb{27, 24, 16}
	mutedColor = rgb{89, 81, 60}
	ruleColor  = rgb{192, 177, 141}
	gridColor  = rgb{214, 208, 194}
	washColor  = rgb{242, 237, 224}
	linkColor  = rgb{110, 38, 32}
)

// The markdown the layout understands: what an answer is written in, minus
// anything it would be guessing at. A line it does not recognise is prose.
var (
	headingLine = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	ruleLine    = regexp.MustCompile(`^([-*_])(\s*[-*_]){2,}$`)
	listLine    = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	chartLine   = regexp.MustCompile(`^!\[[^\]\n]*\]\(chart:([a-z0-9]+)\)$`)
	separator   = regexp.MustCompile(`^:?-+:?$`)
	inline      = regexp.MustCompile("\\*\\*(.+?)\\*\\*|__(.+?)__|`([^`]+)`|\\[([^\\]]+)\\]\\(([^)\\s]+)\\)|\\*([^*\\s](?:[^*]*[^*\\s])?)\\*")
)

type renderer struct {
	pdf    *fpdf.Fpdf
	charts map[string]Chart
	width  float64
	height float64
}

// Render lays the report out on A4. A chart the content embeds but charts
// does not hold is left out.
func Render(doc Document, charts map[string]Chart) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(true, margin)
	pdf.AddUTF8FontFromBytes(sans, "", goregular.TTF)
	pdf.AddUTF8FontFromBytes(sans, "B", gobold.TTF)
	pdf.AddUTF8FontFromBytes(sans, "I", goitalic.TTF)
	pdf.AddUTF8FontFromBytes(sans, "BI", gobolditalic.TTF)
	pdf.AddUTF8FontFromBytes(mono, "", gomono.TTF)
	pdf.SetTitle(doc.Title, true)
	pdf.SetCreator("Lemmary", true)
	pdf.AliasNbPages("")

	pageW, pageH := pdf.GetPageSize()
	r := &renderer{pdf: pdf, charts: charts, width: pageW - 2*margin, height: pageH}
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont(sans, "", 8)
		r.color(mutedColor)
		pdf.CellFormat(0, 4, fmt.Sprintf("%d / {nb}", pdf.PageNo()), "", 0, "C", false, 0, "")
	})

	pdf.AddPage()
	pdf.SetFont(sans, "B", 18)
	r.color(inkColor)
	pdf.MultiCell(0, 8, doc.Title, "", "L", false)
	pdf.Ln(4)
	r.markdown(chartsOnOwnLines(doc.Content))

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (r *renderer) color(c rgb) { r.pdf.SetTextColor(c.r, c.g, c.b) }

// room starts a new page unless h millimetres still fit on this one.
func (r *renderer) room(h float64) {
	if r.pdf.GetY()+h > r.height-margin {
		r.pdf.AddPage()
	}
}

func (r *renderer) markdown(src string) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var prose []string
	flush := func() {
		if len(prose) > 0 {
			r.paragraph(strings.Join(prose, " "))
			prose = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "```"):
			flush()
			end := blockEnd(lines, i+1, func(l string) bool { return !strings.HasPrefix(l, "```") })
			r.code(lines[i+1 : end])
			i = end
		case strings.HasPrefix(line, "|"):
			flush()
			end := blockEnd(lines, i, func(l string) bool { return strings.HasPrefix(l, "|") })
			r.table(lines[i:end])
			i = end - 1
		default:
			if lay := r.blockFor(lines[i], line); lay != nil {
				flush()
				lay()
			} else {
				prose = append(prose, line)
			}
		}
	}
	flush()
}

// chartsOnOwnLines moves every chart embed onto a line of its own, which is
// where the layout draws one, wherever in a line the report put it.
func chartsOnOwnLines(src string) string {
	return ChartEmbed.ReplaceAllString(src, "\n\n$0\n\n")
}

// blockEnd is the index of the first line from start on that in does not
// hold for, or len(lines).
func blockEnd(lines []string, start int, in func(string) bool) int {
	for i := start; i < len(lines); i++ {
		if !in(strings.TrimSpace(lines[i])) {
			return i
		}
	}
	return len(lines)
}

// blockFor is what lays out line when it is a one-line block, or nil when it
// is prose.
func (r *renderer) blockFor(raw, line string) func() {
	if m := headingLine.FindStringSubmatch(line); m != nil {
		return func() { r.heading(len(m[1]), m[2]) }
	}
	if ruleLine.MatchString(line) {
		return r.rule
	}
	if m := chartLine.FindStringSubmatch(line); m != nil {
		return func() {
			if chart, ok := r.charts[m[1]]; ok {
				r.chart(chart)
			}
		}
	}
	if m := listLine.FindStringSubmatch(raw); m != nil {
		return func() { r.listItem(len(m[1])/2, m[2], m[3]) }
	}
	if strings.HasPrefix(line, ">") {
		return func() { r.quote(strings.TrimSpace(strings.TrimLeft(line, "> "))) }
	}
	return nil
}

func (r *renderer) heading(level int, text string) {
	size := map[int]float64{1: 15, 2: 13}[level]
	if size == 0 {
		size = 11.5
	}
	r.room(20)
	r.pdf.Ln(2)
	r.pdf.SetFont(sans, "B", size)
	r.color(inkColor)
	r.pdf.MultiCell(0, size*0.5, plain(text), "", "L", false)
	r.pdf.Ln(1.5)
}

func (r *renderer) paragraph(text string) {
	r.pdf.SetFont(sans, "", bodySize)
	r.inline(text, "")
	r.pdf.Ln(lineHeight + 2.5)
}

func (r *renderer) listItem(level int, marker, text string) {
	indent := margin + 3 + float64(level)*5
	if !strings.ContainsAny(marker, "0123456789") {
		marker = "•"
	}
	r.pdf.SetFont(sans, "", bodySize)
	r.color(inkColor)
	r.pdf.SetX(indent)
	r.pdf.Write(lineHeight, marker)
	r.pdf.SetLeftMargin(indent + 6)
	r.pdf.SetX(indent + 6)
	r.inline(text, "")
	r.pdf.SetLeftMargin(margin)
	r.pdf.Ln(lineHeight + 1)
}

func (r *renderer) quote(text string) {
	y := r.pdf.GetY()
	r.pdf.SetLeftMargin(margin + 5)
	r.pdf.SetX(margin + 5)
	r.pdf.SetFont(sans, "I", bodySize)
	r.inline(text, "I")
	r.pdf.Ln(lineHeight)
	r.pdf.SetLeftMargin(margin)
	r.pdf.SetDrawColor(ruleColor.r, ruleColor.g, ruleColor.b)
	r.pdf.SetLineWidth(0.6)
	r.pdf.Line(margin+1, y, margin+1, r.pdf.GetY())
	r.pdf.Ln(2.5)
}

func (r *renderer) rule() {
	r.pdf.Ln(1)
	r.pdf.SetDrawColor(ruleColor.r, ruleColor.g, ruleColor.b)
	r.pdf.SetLineWidth(0.2)
	r.pdf.Line(margin, r.pdf.GetY(), margin+r.width, r.pdf.GetY())
	r.pdf.Ln(4)
}

func (r *renderer) code(lines []string) {
	r.pdf.SetFont(mono, "", 8.5)
	r.color(inkColor)
	r.pdf.SetFillColor(washColor.r, washColor.g, washColor.b)
	r.pdf.MultiCell(0, 4.2, strings.Join(lines, "\n"), "", "L", true)
	r.pdf.Ln(3)
}

func (r *renderer) table(lines []string) {
	var rows [][]string
	cols := 0
	for _, line := range lines {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = plain(strings.TrimSpace(cells[i]))
		}
		if isSeparatorRow(cells) {
			continue
		}
		rows = append(rows, cells)
		cols = max(cols, len(cells))
	}
	w := r.width / float64(max(cols, 1))
	for i, row := range rows {
		r.tableRow(row, cols, w, i == 0)
	}
	r.pdf.Ln(3)
}

func isSeparatorRow(cells []string) bool {
	for _, c := range cells {
		if !separator.MatchString(c) {
			return false
		}
	}
	return true
}

func (r *renderer) tableRow(row []string, cols int, w float64, header bool) {
	const lh = 4.4
	style := ""
	if header {
		style = "B"
	}
	r.pdf.SetFont(sans, style, 8.5)
	r.color(inkColor)
	split := make([][]string, cols)
	lines := 1
	for c := range cols {
		if c < len(row) {
			split[c] = r.pdf.SplitText(row[c], w)
		}
		lines = max(lines, len(split[c]))
	}
	h := float64(lines)*lh + 2
	r.room(h)
	y := r.pdf.GetY()
	if header {
		r.pdf.SetFillColor(washColor.r, washColor.g, washColor.b)
		r.pdf.Rect(margin, y, r.width, h, "F")
	}
	for c := range cols {
		r.pdf.SetXY(margin+float64(c)*w, y+1)
		r.pdf.MultiCell(w, lh, strings.Join(split[c], "\n"), "", "L", false)
	}
	r.pdf.SetDrawColor(ruleColor.r, ruleColor.g, ruleColor.b)
	r.pdf.SetLineWidth(0.2)
	r.pdf.Line(margin, y+h, margin+r.width, y+h)
	r.pdf.SetXY(margin, y+h)
}

// inline writes text with its bold, italic, code and links, continuing from
// the current position the way prose wraps.
func (r *renderer) inline(text, style string) {
	pos := 0
	for _, m := range inline.FindAllStringSubmatchIndex(text, -1) {
		r.write(text[pos:m[0]], style)
		switch {
		case m[2] >= 0:
			r.write(text[m[2]:m[3]], withStyle(style, "B"))
		case m[4] >= 0:
			r.write(text[m[4]:m[5]], withStyle(style, "B"))
		case m[6] >= 0:
			r.pdf.SetFont(mono, "", bodySize*0.9)
			r.pdf.Write(lineHeight, text[m[6]:m[7]])
		case m[8] >= 0:
			r.link(text[m[8]:m[9]], text[m[10]:m[11]], style)
		default:
			r.write(text[m[12]:m[13]], withStyle(style, "I"))
		}
		pos = m[1]
	}
	r.write(text[pos:], style)
}

func (r *renderer) write(text, style string) {
	if text == "" {
		return
	}
	r.pdf.SetFont(sans, style, bodySize)
	r.color(inkColor)
	r.pdf.Write(lineHeight, text)
}

// link keeps a web link clickable. An in-app link -- a cited document, another
// report -- means nothing outside the app, so only its text is kept.
func (r *renderer) link(text, target, style string) {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		r.write(text, style)
		return
	}
	r.pdf.SetFont(sans, style, bodySize)
	r.color(linkColor)
	r.pdf.WriteLinkString(lineHeight, text, target)
}

func withStyle(style, add string) string {
	if strings.Contains(style, add) {
		return style
	}
	if style+add == "IB" {
		return "BI"
	}
	return style + add
}

// plain is text with its inline markup taken off, for the places that set it
// in one style: headings and table cells.
func plain(text string) string {
	return inline.ReplaceAllStringFunc(text, func(m string) string {
		parts := inline.FindStringSubmatch(m)
		for _, i := range []int{1, 2, 3, 4, 6} {
			if parts[i] != "" {
				return parts[i]
			}
		}
		return m
	})
}
