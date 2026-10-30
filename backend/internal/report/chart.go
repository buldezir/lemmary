// Package report holds what a research run makes besides its answer: the
// charts it draws and the PDF reports it writes. Both are named by their
// content, so the call that made one, read back out of a stored conversation,
// names it again; nothing else stores them.
package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

const (
	MaxPoints = 100
	// MaxSeries is the categorical palette's length: a ninth series would
	// need a colour that is not in it.
	MaxSeries = 8
	// MaxValue bounds a charted value: past it, float axis steps stop being
	// exact, and no figure in a personal archive is that large.
	MaxValue = 1e15
	// MaxContentBytes bounds one report's markdown. The report is stored as
	// the arguments of the call that wrote it, escaped twice over, and has to
	// fit chat.MaxToolCallsJSONBytes with room for the turn's other calls.
	MaxContentBytes = 150_000
)

var ChartTypes = []string{"bar", "line", "pie"}

type Chart struct {
	Type   string   `json:"type"`
	Title  string   `json:"title"`
	Labels []string `json:"labels"`
	Series []Series `json:"series"`
}

type Series struct {
	Name   string    `json:"name,omitempty"`
	Values []float64 `json:"values"`
}

// Normalize trims the chart and returns the first reason it cannot be drawn,
// worded for the model that sent it.
func (c *Chart) Normalize() error {
	c.Type = strings.ToLower(strings.TrimSpace(c.Type))
	c.Title = strings.TrimSpace(c.Title)
	if !slices.Contains(ChartTypes, c.Type) {
		return fmt.Errorf("type must be one of %s", strings.Join(ChartTypes, ", "))
	}
	if c.Title == "" {
		return errors.New("title is required")
	}
	if len(c.Labels) == 0 || len(c.Labels) > MaxPoints {
		return fmt.Errorf("labels must have 1 to %d entries", MaxPoints)
	}
	for i := range c.Labels {
		c.Labels[i] = strings.TrimSpace(c.Labels[i])
	}
	if len(c.Series) == 0 || len(c.Series) > MaxSeries {
		return fmt.Errorf("series must have 1 to %d entries", MaxSeries)
	}
	for i := range c.Series {
		s := &c.Series[i]
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" && len(c.Series) > 1 {
			return errors.New("every series needs a name when there are several")
		}
		if len(s.Values) != len(c.Labels) {
			return fmt.Errorf("series %d has %d values for %d labels", i+1, len(s.Values), len(c.Labels))
		}
		for _, v := range s.Values {
			if math.IsNaN(v) || math.Abs(v) > MaxValue {
				return fmt.Errorf("series %d has a value that is not a number between -1e15 and 1e15", i+1)
			}
		}
	}
	if c.Type == "pie" {
		return c.checkPie()
	}
	return nil
}

func (c *Chart) checkPie() error {
	if len(c.Series) != 1 {
		return errors.New("a pie chart takes exactly one series")
	}
	if len(c.Labels) > MaxSeries {
		return fmt.Errorf("a pie chart takes at most %d slices; fold the smallest into one", MaxSeries)
	}
	total := 0.0
	for _, v := range c.Series[0].Values {
		if v < 0 {
			return errors.New("a pie chart cannot show negative values")
		}
		total += v
	}
	if total <= 0 {
		return errors.New("a pie chart needs a total above zero")
	}
	return nil
}

func (c Chart) ID() string { return contentID("c", c) }

type Document struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (d *Document) Normalize() error {
	d.Title = strings.TrimSpace(d.Title)
	d.Content = strings.TrimSpace(d.Content)
	switch {
	case d.Title == "":
		return errors.New("title is required")
	case d.Content == "":
		return errors.New("content is required")
	case len(d.Content) > MaxContentBytes:
		return fmt.Errorf("content is longer than %d bytes", MaxContentBytes)
	}
	return nil
}

func (d Document) ID() string { return contentID("p", d) }

func contentID(prefix string, v any) string {
	encoded, _ := json.Marshal(v)
	sum := sha256.Sum256(encoded)
	return prefix + hex.EncodeToString(sum[:5])
}

// ChartEmbed is how markdown places a chart: an image whose source is
// chart:<id>.
var ChartEmbed = regexp.MustCompile(`!\[([^\]\n]*)\]\(chart:([a-z0-9]+)\)`)

// ChartRefs lists the chart ids the markdown embeds.
func ChartRefs(markdown string) []string {
	var ids []string
	for _, m := range ChartEmbed.FindAllStringSubmatch(markdown, -1) {
		ids = append(ids, m[2])
	}
	return ids
}
