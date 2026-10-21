package config

import (
	"testing"
	"time"
)

func TestProcessingWindowContains(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, time.March, day, hour, minute, 0, 0, berlin)
	}
	day := ProcessingWindow{Enabled: true, Start: "09:00", End: "17:30", Timezone: "Europe/Berlin"}
	night := ProcessingWindow{Enabled: true, Start: "22:00", End: "07:00", Timezone: "Europe/Berlin"}

	for _, tc := range []struct {
		name   string
		window ProcessingWindow
		at     time.Time
		want   bool
	}{
		{"off is always open", ProcessingWindow{Start: "09:00", End: "10:00"}, at(10, 3, 0), true},
		{"before a day window", day, at(10, 8, 59), false},
		{"day window start is inside", day, at(10, 9, 0), true},
		{"day window end is outside", day, at(10, 17, 30), false},
		{"evening in a night window", night, at(10, 23, 15), true},
		{"early morning in a night window", night, at(10, 6, 59), true},
		{"night window end is outside", night, at(10, 7, 0), false},
		{"midday outside a night window", night, at(10, 12, 0), false},
		// 2026-03-29 is the spring-forward Sunday: 23:30 is still wall-clock
		// 23:30 there, an hour less of UTC offset than the day before.
		{"after the DST switch", night, at(29, 23, 30), true},
		{"compared in the window's zone, not the caller's", night, at(10, 23, 30).UTC(), true},
		{"empty zone is UTC", ProcessingWindow{Enabled: true, Start: "22:00", End: "23:00"}, time.Date(2026, 3, 10, 22, 30, 0, 0, time.UTC), true},
		{"unparsable window does not hold the queue", ProcessingWindow{Enabled: true, Start: "late", End: "07:00"}, at(10, 12, 0), true},
		{"equal ends do not hold the queue", ProcessingWindow{Enabled: true, Start: "07:00", End: "07:00"}, at(10, 12, 0), true},
	} {
		if got := tc.window.Contains(tc.at); got != tc.want {
			t.Errorf("%s: Contains(%s) = %v, want %v", tc.name, tc.at, got, tc.want)
		}
	}
}
