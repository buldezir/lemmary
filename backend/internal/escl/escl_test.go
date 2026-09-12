package escl

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeScanner is an eSCL device: it accepts one job, hands back the pages it
// was given one NextDocument at a time, then reports the end.
type fakeScanner struct {
	server *httptest.Server

	mu       sync.Mutex
	pages    [][]byte
	served   int
	deleted  bool
	settings string
	failPage bool
	notReady int
}

func newFakeScanner(t *testing.T, pages ...string) *fakeScanner {
	t.Helper()
	// The guard refuses loopback in production, and httptest has nowhere else
	// to listen.
	SetAllowLoopback(true)
	t.Cleanup(func() { SetAllowLoopback(false) })

	scanner := &fakeScanner{}
	for _, page := range pages {
		scanner.pages = append(scanner.pages, []byte(page))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/eSCL/ScanJobs", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 4096)
		n, _ := r.Body.Read(body)
		scanner.mu.Lock()
		scanner.settings = string(body[:n])
		scanner.mu.Unlock()

		w.Header().Set("Location", "/eSCL/ScanJobs/job-1")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/eSCL/ScanJobs/job-1/NextDocument", func(w http.ResponseWriter, r *http.Request) {
		scanner.mu.Lock()
		defer scanner.mu.Unlock()
		if scanner.failPage {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if scanner.notReady > 0 {
			scanner.notReady--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if scanner.served >= len(scanner.pages) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		page := scanner.pages[scanner.served]
		scanner.served++
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(page)
	})
	mux.HandleFunc("/eSCL/ScanJobs/job-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		scanner.mu.Lock()
		scanner.deleted = true
		scanner.mu.Unlock()
	})

	scanner.server = httptest.NewServer(mux)
	t.Cleanup(scanner.server.Close)
	return scanner
}

func (f *fakeScanner) wasDeleted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleted
}

func (f *fakeScanner) sentSettings() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings
}

func TestScanCollectsEveryPageFromTheFeeder(t *testing.T) {
	scanner := newFakeScanner(t, "page one", "page two", "page three")

	pages, err := Scan(context.Background(), scanner.server.URL, Feeder, maxScanBytes)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("pages=%d want 3", len(pages))
	}
	if string(pages[0]) != "page one" || string(pages[2]) != "page three" {
		t.Fatalf("pages came back out of order: %q", pages)
	}
	// A job the scanner still thinks is open makes it refuse the next one.
	if !scanner.wasDeleted() {
		t.Fatal("the scan job was not deleted")
	}
	if !strings.Contains(scanner.sentSettings(), "<pwg:InputSource>Feeder</pwg:InputSource>") {
		t.Fatalf("settings did not ask for the feeder: %s", scanner.sentSettings())
	}
}

// Some devices answer a second NextDocument by scanning the glass again, so the
// platen has to stop after one page rather than waiting for the 404.
func TestScanTakesOnePageFromThePlaten(t *testing.T) {
	scanner := newFakeScanner(t, "page one", "page two")

	pages, err := Scan(context.Background(), scanner.server.URL, Platen, maxScanBytes)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages=%d want 1", len(pages))
	}
	if !scanner.wasDeleted() {
		t.Fatal("the scan job was not deleted")
	}
}

func TestScanDeletesTheJobWhenAPageFails(t *testing.T) {
	scanner := newFakeScanner(t, "page one")
	scanner.failPage = true

	if _, err := Scan(context.Background(), scanner.server.URL, Platen, maxScanBytes); err == nil {
		t.Fatal("expected an error")
	}
	// This is the case that matters: a device left holding a failed job refuses
	// every later scan until it is power-cycled.
	if !scanner.wasDeleted() {
		t.Fatal("the scan job was not deleted after a failure")
	}
}

func TestScanReportsAnEmptyFeeder(t *testing.T) {
	scanner := newFakeScanner(t)

	_, err := Scan(context.Background(), scanner.server.URL, Feeder, maxScanBytes)
	if err == nil {
		t.Fatal("expected an error for a scanner with no pages")
	}
	if !strings.Contains(err.Error(), "no pages") {
		t.Fatalf("error=%q want it to mention no pages", err)
	}
}

func TestScanStopsWhenThePagesExceedTheBudget(t *testing.T) {
	scanner := newFakeScanner(t, strings.Repeat("x", 64), strings.Repeat("y", 64))

	if _, err := Scan(context.Background(), scanner.server.URL, Feeder, 32); err == nil {
		t.Fatal("expected an error once the budget was passed")
	}
}

func TestScanRefusesAnAddressOffTheLocalNetwork(t *testing.T) {
	for _, address := range []string{
		"8.8.8.8",                // public
		"169.254.169.254",        // cloud metadata
		"127.0.0.1",              // the host's own services
		"http://scanner.example", // a name, which could resolve anywhere
		"ftp://192.168.1.9",      // not a scheme we speak
		"",                       // nothing at all
	} {
		if _, err := Scan(context.Background(), address, Platen, maxScanBytes); err == nil {
			t.Fatalf("%q should have been refused", address)
		}
	}
}

func TestNormalizeBaseFillsInTheUsualParts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"192.168.1.9":               "http://192.168.1.9/eSCL",
		"http://192.168.1.9":        "http://192.168.1.9/eSCL",
		"http://192.168.1.9/":       "http://192.168.1.9/eSCL",
		"http://192.168.1.9:8080":   "http://192.168.1.9:8080/eSCL",
		"https://10.0.0.4/airscan/": "https://10.0.0.4/airscan",
	}
	for input, want := range cases {
		got, err := normalizeBase(input)
		if err != nil {
			t.Fatalf("normalizeBase(%q) error: %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeBase(%q)=%q want %q", input, got, want)
		}
	}
}

// The job address comes from the device, so it is the one part of the exchange
// that could point somewhere else -- and the one devices spell in every
// possible way. Whatever they send, the request goes to the scanner we dialed.
func TestResolveJobURLKeepsTheScannerWeDialed(t *testing.T) {
	t.Parallel()

	const base = "http://192.168.1.9/eSCL"
	const want = "http://192.168.1.9/eSCL/ScanJobs/1"
	for _, location := range []string{
		"/eSCL/ScanJobs/1",                       // root-relative
		"ScanJobs/1",                             // bare, against the ScanJobs URL
		"http://192.168.1.9/eSCL/ScanJobs/1",     // absolute
		"http://192.168.1.9:80/eSCL/ScanJobs/1",  // the same host, spelled out
		"http://BRW123456.local/eSCL/ScanJobs/1", // its own mDNS name
		"http://localhost/eSCL/ScanJobs/1",       // the HP quirk
		"https://example.com/eSCL/ScanJobs/1",    // and somewhere else entirely
	} {
		got, err := resolveJobURL(base, location)
		if err != nil {
			t.Fatalf("resolveJobURL(%q) error: %v", location, err)
		}
		if got != want {
			t.Fatalf("resolveJobURL(%q)=%q want %q", location, got, want)
		}
	}
}

// 503 from NextDocument is "not yet", not "no": the head is still moving or the
// feeder is picking up the next sheet.
func TestScanWaitsOutAScannerThatIsNotReady(t *testing.T) {
	scanner := newFakeScanner(t, "page one")
	scanner.notReady = 3
	notReadyDelay = time.Millisecond
	t.Cleanup(func() { notReadyDelay = 2 * time.Second })

	pages, err := Scan(context.Background(), scanner.server.URL, Platen, maxScanBytes)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(pages) != 1 || string(pages[0]) != "page one" {
		t.Fatalf("pages=%q want the one page", pages)
	}
}

// The budget has to bound the read itself, not just the total afterwards: a
// device answering with something enormous must not land on the heap first.
func TestScanDoesNotReadPastTheBudget(t *testing.T) {
	SetAllowLoopback(true)
	t.Cleanup(func() { SetAllowLoopback(false) })

	var served int64
	mux := http.NewServeMux()
	mux.HandleFunc("/eSCL/ScanJobs", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Location", "/eSCL/ScanJobs/job-1")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/eSCL/ScanJobs/job-1/NextDocument", func(w http.ResponseWriter, r *http.Request) {
		// No Content-Length: the client cannot know what it is in for. 64 MB of
		// it, which is what the client would hold without a bounded read.
		page := make([]byte, 64<<10)
		for range 1024 {
			n, err := w.Write(page)
			atomic.AddInt64(&served, int64(n))
			if err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/eSCL/ScanJobs/job-1", func(w http.ResponseWriter, r *http.Request) {})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	if _, err := Scan(context.Background(), server.URL, Platen, 8<<10); err == nil {
		t.Fatal("expected the scan to be refused for going over the budget")
	}
	// The client stops reading a page past the budget, so the device gets no
	// further than a socket buffer or two of the 64 MB it wanted to send.
	if got := atomic.LoadInt64(&served); got > 4<<20 {
		t.Fatalf("the scanner got to send %d bytes against an 8 KB budget", got)
	}
}

func TestParseSource(t *testing.T) {
	t.Parallel()

	if source, err := ParseSource(""); err != nil || source != Platen {
		t.Fatalf("empty source=%q err=%v want the platen", source, err)
	}
	if source, err := ParseSource("adf"); err != nil || source != Feeder {
		t.Fatalf("adf=%q err=%v want the feeder", source, err)
	}
	if _, err := ParseSource("fax"); err == nil {
		t.Fatal("expected an error for an unknown source")
	}
}
