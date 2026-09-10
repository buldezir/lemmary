package metrics

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAddrFromEnv(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"   ":              "",
		"9464":             ":9464",
		":9464":            ":9464",
		"127.0.0.1:9464":   "127.0.0.1:9464",
		"0.0.0.0:9464":     "0.0.0.0:9464",
		"[::1]:9464":       "[::1]:9464",
		" 127.0.0.1:9464 ": "127.0.0.1:9464",
	}
	for in, want := range cases {
		t.Setenv(EnvAddr, in)
		if got := addrFromEnv(); got != want {
			t.Errorf("addrFromEnv() with %q = %q, want %q", in, got, want)
		}
	}
}

// TestScrape is the check that fails if the exporter wiring breaks: it records
// through the same package-level instruments every call site uses, then reads
// the endpoint the way a scraper would.
func TestScrape(t *testing.T) {
	url, stop, err := start("127.0.0.1:0", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		stop(ctx)
	})

	Job("completed", 2*time.Second)
	TimeAICall(context.Background(), "ocr", "mistral", "mistral-ocr-latest")(nil)
	AITokens("gpt-5", 100, 10, 20)
	QueueDepth(func() int64 { return 7 })
	RegisterUsage(func() (Usage, error) {
		return Usage{
			Counts:      map[string]int64{"documents": 42, "additional_users": 3},
			CountLimits: map[string]int64{"documents": 1000},
			Bytes:       map[string]int64{"storage_bytes": 12345},
			ByteLimits:  map[string]int64{"storage_bytes": 5368709120},
		}, nil
	})

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	page := string(body)

	for _, want := range []string{
		// Ours. The _seconds suffix is the exporter reading metric.WithUnit("s").
		`lemmary_job_duration_seconds_count{`,
		`outcome="completed"`,
		`lemmary_ai_call_duration_seconds_count{`,
		`kind="ocr"`,
		`lemmary_ai_tokens_total{`,
		"lemmary_jobs_pending 7",
		// Usage and its allowance share the resource label, so a dashboard
		// divides one by the other. An unset limit emits no series at all --
		// asserted in TestUsageOmitsUnsetLimits below.
		`lemmary_usage{resource="documents"} 42`,
		`lemmary_usage{resource="additional_users"} 3`,
		`lemmary_limit{resource="documents"} 1000`,
		`lemmary_usage_bytes{resource="storage_bytes"} 12345`,
		`lemmary_limit_bytes{resource="storage_bytes"} 5.36870912e+09`,
		// Not ours, and the point: the otel Prometheus exporter registers into
		// client_golang's default registry, which already carries the Go and
		// process collectors. This is what says we still get runtime metrics
		// without an instrumentation package for them -- if the exporter ever
		// stops defaulting to that registry, this line is the warning.
		"go_goroutines",
		"process_open_fds",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("scrape body is missing %q; body was:\n%s", want, page)
		}
	}

	// An unset allowance emits nothing at all -- not a zero, which would read
	// as "none permitted", and not a sentinel every query would have to know
	// about. The read above supplied a limit for documents and storage only.
	for _, unwanted := range []string{
		`lemmary_limit{resource="additional_users"}`,
		`lemmary_limit_bytes{resource="file_bytes"}`,
	} {
		if strings.Contains(page, unwanted) {
			t.Errorf("scrape body has %q for an unset limit; body was:\n%s", unwanted, page)
		}
	}
}

// TestRegisterOffWhenUnset is the promise that absent means off: no provider
// installed, no port bound, nothing to reach.
func TestRegisterOffWhenUnset(t *testing.T) {
	t.Setenv(EnvAddr, "")
	if addr := addrFromEnv(); addr != "" {
		t.Fatalf("addrFromEnv() = %q with %s unset, want empty", addr, EnvAddr)
	}
}
