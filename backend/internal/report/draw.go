package report

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"codeberg.org/go-pdf/fpdf"
)

// palette is the categorical order, fixed: a series keeps its colour by its
// position, never by its rank. frontend/src/components/Chart.tsx draws with
// the same eight.
var palette = []rgb{
	{42, 120, 214}, {235, 104, 52}, {27, 175, 122}, {237, 161, 0},
	{232, 123, 164}, {0, 131, 0}, {98, 80, 214}, {227, 73, 72},
}

const (
	chartHeight = 62.0
	tickSize    = 7.0
	// valueLabelLimit is how many bars still get their value written on top.
	valueLabelLimit = 16
)

func (r *renderer) fill(c rgb) { r.pdf.SetFillColor(c.r, c.g, c.b) }

func (r *renderer) chart(c Chart) {
	r.room(chartHeight + 18)
	r.pdf.Ln(1)
	r.pdf.SetFont(sans, "B", 10.5)
	r.color(inkColor)
	r.pdf.MultiCell(0, 5, c.Title, "", "L", false)
	y := r.pdf.GetY() + 1
	bottom := y + chartHeight
	if c.Type == "pie" {
		bottom = r.pie(c, y)
	} else {
		r.axes(c, y)
		if len(c.Series) > 1 {
			names := make([]string, len(c.Series))
			for i, s := range c.Series {
				names[i] = s.Name
			}
			bottom = r.legend(names, bottom+2)
		}
	}
	r.pdf.SetXY(margin, bottom)
	r.pdf.Ln(5)
}

func (r *renderer) axes(c Chart, y float64) {
	ticks := niceTicks(extent(c))
	lo, hi := ticks[0], ticks[len(ticks)-1]
	r.pdf.SetFont(sans, "", tickSize)
	gutter := 0.0
	for _, t := range ticks {
		gutter = max(gutter, r.pdf.GetStringWidth(formatNumber(t)))
	}
	plotX, plotW := margin+gutter+2, r.width-gutter-2
	plotY, plotH := y+2, chartHeight-10
	scale := func(v float64) float64 { return plotY + plotH - (v-lo)/(hi-lo)*plotH }

	r.pdf.SetDrawColor(gridColor.r, gridColor.g, gridColor.b)
	r.pdf.SetLineWidth(0.15)
	r.color(mutedColor)
	for _, t := range ticks {
		label := formatNumber(t)
		r.pdf.Line(plotX, scale(t), plotX+plotW, scale(t))
		r.pdf.Text(plotX-1.5-r.pdf.GetStringWidth(label), scale(t)+1, label)
	}
	band := plotW / float64(len(c.Labels))
	r.xLabels(c.Labels, plotX, plotY+plotH, band)
	if c.Type == "bar" {
		r.bars(c, plotX, band, scale)
	} else {
		r.lines(c, plotX, band, scale)
	}
}

// extent is the value range the axis has to show. Bars grow from zero, so
// zero is always in it for them.
func extent(c Chart) (float64, float64) {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range c.Series {
		for _, v := range s.Values {
			lo, hi = min(lo, v), max(hi, v)
		}
	}
	if c.Type == "bar" {
		lo, hi = min(lo, 0), max(hi, 0)
	}
	return lo, hi
}

func (r *renderer) xLabels(labels []string, x, y, band float64) {
	r.pdf.SetFont(sans, "", tickSize)
	r.color(mutedColor)
	widest := 0.0
	for _, l := range labels {
		widest = max(widest, r.pdf.GetStringWidth(l))
	}
	every := max(1, int(math.Ceil((min(widest, 30)+2)/band)))
	for i := 0; i < len(labels); i += every {
		label := r.fit(labels[i], band*float64(every)-1)
		r.pdf.Text(x+(float64(i)+0.5)*band-r.pdf.GetStringWidth(label)/2, y+4, label)
	}
}

func (r *renderer) bars(c Chart, x, band float64, scale func(float64) float64) {
	n := float64(len(c.Series))
	w, gap := barWidth(band, len(c.Series))
	start := (band - w*n) / 2
	base := scale(0)
	labelled := len(c.Series) == 1 && len(c.Labels) <= valueLabelLimit
	for s, series := range c.Series {
		r.fill(palette[s])
		for i, v := range series.Values {
			bx := x + float64(i)*band + start + float64(s)*w
			top, h := min(scale(v), base), math.Abs(base-scale(v))
			corners := "12"
			if v < 0 {
				corners = "34"
			}
			if h > 0 {
				r.pdf.RoundedRect(bx+gap/2, top, w-gap, h, min(0.8, (w-gap)/2, h), corners, "F")
			}
			if labelled {
				r.pdf.SetFont(sans, "", 6.5)
				r.color(inkColor)
				label := formatNumber(v)
				r.pdf.Text(bx+w/2-r.pdf.GetStringWidth(label)/2, top-1, label)
			}
		}
	}
}

func (r *renderer) lines(c Chart, x, band float64, scale func(float64) float64) {
	r.pdf.SetLineWidth(0.6)
	r.pdf.SetLineCapStyle("round")
	r.pdf.SetLineJoinStyle("round")
	for s, series := range c.Series {
		col := palette[s]
		r.pdf.SetDrawColor(col.r, col.g, col.b)
		r.fill(col)
		points := make([]fpdf.PointType, len(series.Values))
		for i, v := range series.Values {
			points[i] = fpdf.PointType{X: x + (float64(i)+0.5)*band, Y: scale(v)}
		}
		for i := 1; i < len(points); i++ {
			r.pdf.Line(points[i-1].X, points[i-1].Y, points[i].X, points[i].Y)
		}
		if len(points) <= 24 {
			for _, p := range points {
				r.pdf.Circle(p.X, p.Y, 0.9, "F")
			}
		}
	}
}

// pie draws the slices clockwise from the top with the legend to their right,
// and returns where the chart ends.
func (r *renderer) pie(c Chart, y float64) float64 {
	values := c.Series[0].Values
	total := 0.0
	for _, v := range values {
		total += v
	}
	radius := (chartHeight - 6) / 2
	cx, cy := margin+radius+2, y+radius+1
	angle := -90.0
	r.pdf.SetDrawColor(255, 255, 255)
	r.pdf.SetLineWidth(0.5)
	for i, v := range values {
		sweep := v / total * 360
		if sweep <= 0 {
			continue
		}
		steps := int(math.Ceil(sweep / 3))
		points := []fpdf.PointType{{X: cx, Y: cy}}
		for k := 0; k <= steps; k++ {
			a := (angle + sweep*float64(k)/float64(steps)) * math.Pi / 180
			points = append(points, fpdf.PointType{X: cx + radius*math.Cos(a), Y: cy + radius*math.Sin(a)})
		}
		r.fill(palette[i])
		r.pdf.Polygon(points, "DF")
		angle += sweep
	}

	r.pdf.SetFont(sans, "", 8)
	r.color(inkColor)
	lx, ly := cx+radius+10, y+6
	for i, label := range c.Labels {
		r.fill(palette[i])
		r.pdf.Rect(lx, ly-2.4, 2.6, 2.6, "F")
		text := fmt.Sprintf("%s  %s · %s%%", label, formatNumber(values[i]), compact(values[i]/total*100))
		r.pdf.Text(lx+4, ly, r.fit(text, margin+r.width-lx-4))
		ly += 5.5
	}
	return max(y+chartHeight, ly)
}

// legend writes a swatch and a name per series, wrapping, from y down, and
// returns where it ends.
func (r *renderer) legend(names []string, y float64) float64 {
	r.pdf.SetFont(sans, "", 8)
	r.color(inkColor)
	y += 3
	x := margin
	for i, name := range names {
		w := r.pdf.GetStringWidth(name) + 9
		if x > margin && x+w > margin+r.width {
			x, y = margin, y+5
		}
		r.fill(palette[i])
		r.pdf.Rect(x, y-2.4, 2.6, 2.6, "F")
		r.pdf.Text(x+4, y, name)
		x += w
	}
	return y + 2
}

func (r *renderer) fit(text string, w float64) string {
	if r.pdf.GetStringWidth(text) <= w {
		return text
	}
	runes := []rune(text)
	for len(runes) > 1 && r.pdf.GetStringWidth(string(runes)+"…") > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// barWidth is one bar's slot in a band shared by n series, and the gap that
// separates it from its neighbour. The gap shrinks with the bar, so a crowded
// chart still has a bar to draw.
func barWidth(band float64, n int) (w, gap float64) {
	w = min(band*0.72/float64(n), 8)
	return w, min(0.6, w/3)
}

// niceTicks spans lo..hi with about five round steps. A range float steps
// cannot divide -- too wide, too narrow, or too far from zero for its width --
// gets its two ends instead.
func niceTicks(lo, hi float64) []float64 {
	if hi <= lo {
		hi = lo + max(1, math.Abs(lo))
	}
	raw := (hi - lo) / 5
	unit := math.Pow(10, math.Floor(math.Log10(raw)))
	step := 10 * unit
	for _, f := range []float64{1, 2, 5} {
		if raw <= f*unit {
			step = f * unit
			break
		}
	}
	first := math.Floor(lo / step)
	n := math.Ceil(hi/step) - first
	if !(n >= 1 && n <= 20) {
		return []float64{lo, hi}
	}
	ticks := make([]float64, 0, int(n)+1)
	for k := range int(n) + 1 {
		ticks = append(ticks, (first+float64(k))*step)
	}
	if ticks[len(ticks)-1] <= ticks[0] {
		return []float64{lo, hi}
	}
	return ticks
}

// formatNumber is a value as an axis or a label shows it: 950, 1.2K, 3.4M.
func formatNumber(v float64) string {
	for _, u := range []struct {
		div    float64
		suffix string
	}{{1e9, "B"}, {1e6, "M"}, {1e3, "K"}} {
		if math.Abs(v) >= u.div {
			return compact(v/u.div) + u.suffix
		}
	}
	return compact(v)
}

// compact keeps three significant digits and drops trailing zeros.
func compact(v float64) string {
	if v == 0 {
		return "0"
	}
	digits := min(12, max(0, 2-int(math.Floor(math.Log10(math.Abs(v))))))
	s := strconv.FormatFloat(v, 'f', digits, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}
