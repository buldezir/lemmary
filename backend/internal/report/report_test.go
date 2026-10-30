package report

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestChartNormalizeRefusesWhatCannotBeDrawn(t *testing.T) {
	t.Parallel()
	cases := map[string]Chart{
		"type must be":           {Type: "radar", Title: "t", Labels: []string{"a"}, Series: []Series{{Values: []float64{1}}}},
		"title is required":      {Type: "bar", Labels: []string{"a"}, Series: []Series{{Values: []float64{1}}}},
		"1 values for 2":         {Type: "bar", Title: "t", Labels: []string{"a", "b"}, Series: []Series{{Values: []float64{1}}}},
		"needs a name":           {Type: "line", Title: "t", Labels: []string{"a"}, Series: []Series{{Values: []float64{1}}, {Name: "b", Values: []float64{2}}}},
		"exactly one series":     {Type: "pie", Title: "t", Labels: []string{"a"}, Series: []Series{{Name: "a", Values: []float64{1}}, {Name: "b", Values: []float64{2}}}},
		"negative":               {Type: "pie", Title: "t", Labels: []string{"a", "b"}, Series: []Series{{Values: []float64{3, -1}}}},
		"total above zero":       {Type: "pie", Title: "t", Labels: []string{"a"}, Series: []Series{{Values: []float64{0}}}},
		"series must have 1 to":  {Type: "bar", Title: "t", Labels: []string{"a"}},
		"between -1e15 and 1e15": {Type: "line", Title: "t", Labels: []string{"a"}, Series: []Series{{Values: []float64{1e16}}}},
	}
	for want, chart := range cases {
		err := chart.Normalize()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want %q", chart, err, want)
		}
	}
}

func TestChartIDFollowsContentNotSpelling(t *testing.T) {
	t.Parallel()
	a := Chart{Type: " Bar ", Title: " Spend ", Labels: []string{" 2024 ", "2025"}, Series: []Series{{Values: []float64{1, 2}}}}
	b := Chart{Type: "bar", Title: "Spend", Labels: []string{"2024", "2025"}, Series: []Series{{Values: []float64{1, 2}}}}
	if err := a.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	if a.ID() != b.ID() || !strings.HasPrefix(a.ID(), "c") {
		t.Fatalf("ids %q and %q should match", a.ID(), b.ID())
	}
	b.Series[0].Values[1] = 3
	if a.ID() == b.ID() {
		t.Fatal("different data must get a different id")
	}
}

func TestChartRefs(t *testing.T) {
	t.Parallel()
	got := ChartRefs("intro\n\n![Spend](chart:c0123456789)\n\ntext ![x](chart:cabc) and ![no](https://x)")
	if strings.Join(got, ",") != "c0123456789,cabc" {
		t.Fatalf("refs = %v", got)
	}
}

func TestNiceTicksAndNumbers(t *testing.T) {
	t.Parallel()
	ticks := niceTicks(0, 87)
	if !slices.Equal(ticks, []float64{0, 20, 40, 60, 80, 100}) {
		t.Fatalf("ticks = %v", ticks)
	}
	for v, want := range map[float64]string{0: "0", 0.6000000000000001: "0.6", 950: "950", 1234: "1.23K", 12900: "12.9K", -3400000: "-3.4M", 1e-99: "0"} {
		if got := formatNumber(v); got != want {
			t.Errorf("formatNumber(%v) = %q, want %q", v, got, want)
		}
	}
}

// Ranges a float step cannot walk -- a flat series far from zero, a sliver of
// a range at 1e15 -- must still end, with at least two distinct ticks.
func TestNiceTicksEndOnRangesFloatsCannotStep(t *testing.T) {
	t.Parallel()
	for _, r := range [][2]float64{{1e16, 1e16}, {MaxValue, MaxValue}, {-MaxValue, MaxValue}, {1e15, 1e15 + 0.125}, {0, 5e-324}, {7, 7}} {
		ticks := niceTicks(r[0], r[1])
		if len(ticks) < 2 || len(ticks) > 21 || ticks[len(ticks)-1] <= ticks[0] {
			t.Errorf("niceTicks(%v, %v) = %v", r[0], r[1], ticks)
		}
	}
}

func TestBarsKeepAWidthAtTheLimits(t *testing.T) {
	t.Parallel()
	w, gap := barWidth(160.0/MaxPoints, MaxSeries)
	if w <= 0 || w-gap <= 0 {
		t.Fatalf("bar width %v with gap %v at %d labels and %d series", w, gap, MaxPoints, MaxSeries)
	}
}

func TestChartsAreDrawnWhereverALinePlacesThem(t *testing.T) {
	t.Parallel()
	got := chartsOnOwnLines("Spend fell ![Spend](chart:c0123456789) last year.")
	if !slices.Contains(strings.Split(got, "\n"), "![Spend](chart:c0123456789)") {
		t.Fatalf("embed not on a line of its own: %q", got)
	}
}

func TestRenderLaysOutEveryBlockInCyrillicToo(t *testing.T) {
	t.Parallel()
	bar := Chart{Type: "bar", Title: "Расходы по годам", Labels: []string{"2023", "2024", "2025"}, Series: []Series{{Values: []float64{1200, 1850.5, 990}}}}
	line := Chart{Type: "line", Title: "Monthly", Labels: []string{"Jan", "Feb", "Mar", "Apr"}, Series: []Series{{Name: "Power", Values: []float64{30, 42, 38, 51}}, {Name: "Water", Values: []float64{12, 11, 15, 9}}}}
	pie := Chart{Type: "pie", Title: "Share", Labels: []string{"Rent", "Food", "Other"}, Series: []Series{{Values: []float64{50, 30, 20}}}}
	charts := map[string]Chart{}
	for _, c := range []Chart{bar, line, pie} {
		if err := c.Normalize(); err != nil {
			t.Fatal(err)
		}
		charts[c.ID()] = c
	}
	content := "# Сводка\n\nВсего **3 счёта** и *одна* [страница](https://example.com), см. [Счёт](/document/abc) и `код`.\n\n" +
		"## Список\n\n- первый\n- второй\n  - вложенный\n1. one\n2. two\n\n> цитата\n\n---\n\n" +
		"| Год | Сумма |\n|---|---:|\n| 2023 | 1 200 |\n| 2024 | 1 850,50 |\n\n```\nraw text\n```\n\n" +
		"![bar](chart:" + bar.ID() + ")\n\n![line](chart:" + line.ID() + ")\n\n![pie](chart:" + pie.ID() + ")\n\n![gone](chart:cmissing)\n"

	out, err := Render(Document{Title: "Отчёт о расходах", Content: content}, charts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %q", out[:min(len(out), 16)])
	}
	if !bytes.Contains(out, []byte("https://example.com")) {
		t.Error("the web link should stay clickable")
	}
	if path := os.Getenv("REPORT_SAMPLE"); path != "" {
		_ = os.WriteFile(path, out, 0o600)
	}
}
