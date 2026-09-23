package appapi

import (
	"testing"
	"time"
)

func TestScanWindowIncludesTheLastDay(t *testing.T) {
	from, to, err := scanWindow(imapScanRequest{From: "2026-01-01", To: "2026-01-31"})
	if err != nil {
		t.Fatal(err)
	}
	if !from.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %v .. %v", from, to)
	}
	for _, bad := range []imapScanRequest{
		{From: "", To: "2026-01-31"},
		{From: "2026-01-01", To: "31.01.2026"},
		{From: "2026-02-01", To: "2026-01-31"},
	} {
		if _, _, err := scanWindow(bad); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}
