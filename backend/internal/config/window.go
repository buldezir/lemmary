package config

import (
	"fmt"
	"time"
	// The runtime image ships no zoneinfo, and a window in "Europe/Berlin" has
	// to keep its wall-clock hours across DST.
	_ "time/tzdata"
)

// ProcessingWindow is the daily slot new jobs may start in, for an AI server
// that is not up around the clock. A Start after End crosses midnight.
type ProcessingWindow struct {
	Enabled  bool
	Start    string
	End      string
	Timezone string
}

// ParseClock reads "15:04" as minutes after midnight.
func ParseClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("parse clock %q: %w", s, err)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Contains is true at any time when the window is off. A stored window that
// no longer parses is treated as off too, so it cannot hold the queue forever.
func (w ProcessingWindow) Contains(t time.Time) bool {
	if !w.Enabled {
		return true
	}
	start, errStart := ParseClock(w.Start)
	end, errEnd := ParseClock(w.End)
	loc, errLoc := time.LoadLocation(w.Timezone)
	if errStart != nil || errEnd != nil || errLoc != nil || start == end {
		return true
	}
	local := t.In(loc)
	now := local.Hour()*60 + local.Minute()
	if start < end {
		return now >= start && now < end
	}
	return now >= start || now < end
}
