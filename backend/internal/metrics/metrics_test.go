package metrics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
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
	url, stop := startTestEndpoint(t)
	// Stopped, then started again: the second boot in one process must get a
	// live endpoint that still carries everything recorded so far, because the
	// global meter delegates to the first provider and only to it.
	t.Cleanup(func() {
		stop(context.Background())
		again, stopAgain, err := start("127.0.0.1:0", slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatalf("start after stop: %v", err)
		}
		defer stopAgain(context.Background())
		if again == url {
			t.Fatalf("start after stop returned the stopped URL %s", url)
		}
		page := scrape(t, again)
		if !strings.Contains(page, "lemmary_job") {
			t.Errorf("scrape after restart lost the recorded series; body was:\n%s", page)
		}
	})

	HTTPRequest(http.MethodGet, "/api/collections/{collection}/records", 401, 12*time.Millisecond)
	Job("completed", 2*time.Second)
	TimeAICall(context.Background(), "ocr", "mistral", "mistral-ocr-latest")(nil)
	AITokens("gpt-5", 100, 10, 20)
	qd := QueueDepth(func() (int64, error) { return 7, nil })
	usage := RegisterUsage(func() (Usage, error) {
		return Usage{
			Counts:      map[string]int64{"documents": 42, "additional_users": 3},
			CountLimits: map[string]int64{"documents": 1000},
			Bytes:       map[string]int64{"storage_bytes": 12345},
			ByteLimits:  map[string]int64{"storage_bytes": 5368709120},
		}, nil
	})
	t.Cleanup(func() {
		unregister(qd)
		unregister(usage)
	})

	page := scrape(t, url)

	for _, want := range []string{
		// Ours. The _seconds suffix is the exporter reading metric.WithUnit("s").
		// The status is an attribute we compute, not one otelhttp watched the
		// writer for; see appwire.recordRequest for why that distinction is
		// the whole point.
		`http_server_request_duration_seconds_count{`,
		`http_response_status_code="401"`,
		`http_route="/api/collections/{collection}/records"`,
		`lemmary_job_duration_seconds_count{`,
		`outcome="completed"`,
		`lemmary_ai_call_duration_seconds_count{`,
		`kind="ocr"`,
		`lemmary_ai_tokens_total{`,
		"lemmary_jobs_pending 7",
		// Usage and its allowance share the resource label, so a dashboard
		// divides one by the other. An unset limit emits no series at all --
		// asserted below.
		`lemmary_usage{resource="documents"} 42`,
		`lemmary_usage{resource="additional_users"} 3`,
		`lemmary_limit{resource="documents"} 1000`,
		`lemmary_usage_bytes{resource="storage_bytes"} 12345`,
		`lemmary_limit_bytes{resource="storage_bytes"} 5.36870912e+09`,
		// Not ours: the Go and process collectors are registered by hand in
		// buildRegistry(). This is what notices if those two lines go.
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

	// The SDK default histogram bounds are millisecond-scale (0, 5, 10, …,
	// 7500). A le="0.5" bucket on the job and AI instruments is the proof we
	// spelled out seconds; le="7500" would be the default leaking back in.
	for _, metric := range []string{
		"lemmary_job_duration_seconds_bucket",
		"lemmary_ai_call_duration_seconds_bucket",
	} {
		if !metricHasLe(page, metric, "0.5") {
			t.Errorf("%s is missing a second-scale le=\"0.5\" bucket; body was:\n%s", metric, page)
		}
		if metricHasLe(page, metric, "7500") {
			t.Errorf("%s still has the SDK millisecond bound le=\"7500\"; body was:\n%s", metric, page)
		}
	}
}

// TestGaugeErrorOmitsSeries is the promise that a failed reading is a gap, not
// a zero: an unreadable database is not an empty queue or an empty archive.
func TestGaugeErrorOmitsSeries(t *testing.T) {
	url, stop := startTestEndpoint(t)
	t.Cleanup(func() { stop(context.Background()) })

	qd := QueueDepth(func() (int64, error) { return 0, errors.New("db closed") })
	usage := RegisterUsage(func() (Usage, error) { return Usage{}, errors.New("db closed") })
	t.Cleanup(func() {
		unregister(qd)
		unregister(usage)
	})

	page := scrape(t, url)
	for _, unwanted := range []string{
		"lemmary_jobs_pending",
		`lemmary_usage{`,
		`lemmary_limit{`,
		`lemmary_usage_bytes{`,
		`lemmary_limit_bytes{`,
	} {
		if strings.Contains(page, unwanted) {
			t.Errorf("scrape body has %q after a failed read; body was:\n%s", unwanted, page)
		}
	}
}

// TestRegisterOffWhenUnset is the promise that absent means off: no provider
// installed, no port bound, nothing to reach. Register reads the env and
// returns before touching the app, so a nil app is the proof that nothing is
// bound -- a missing early return panics.
func TestRegisterOffWhenUnset(t *testing.T) {
	t.Setenv(EnvAddr, "")
	Register(nil)
}

func startTestEndpoint(t *testing.T) (url string, stop func(context.Context)) {
	t.Helper()
	url, stop, err := start("127.0.0.1:0", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return url, stop
}

func scrape(t *testing.T, url string) string {
	t.Helper()
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
	return string(body)
}

func unregister(reg metric.Registration) {
	if reg != nil {
		_ = reg.Unregister()
	}
}

func metricHasLe(page, metric, le string) bool {
	prefix := metric + "{"
	token := `le="` + le + `"`
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, prefix) && strings.Contains(line, token) {
			return true
		}
	}
	return false
}
