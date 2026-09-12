package escl

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/hashicorp/mdns"
)

const capabilities = `<?xml version="1.0" encoding="UTF-8"?>
<scan:ScannerCapabilities xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03"
                          xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:Version>2.6</pwg:Version>
  <pwg:MakeAndModel>Brother MFC-L2750DW</pwg:MakeAndModel>
</scan:ScannerCapabilities>`

func TestProbeReadsTheModelName(t *testing.T) {
	SetAllowLoopback(true)
	t.Cleanup(func() { SetAllowLoopback(false) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eSCL/ScannerCapabilities" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(capabilities))
	}))
	t.Cleanup(server.Close)

	host := server.Listener.Addr().String()
	model, ok := probe(context.Background(), newClient(), host)
	if !ok {
		t.Fatal("the scanner was not recognized")
	}
	if model != "Brother MFC-L2750DW" {
		t.Fatalf("model=%q", model)
	}
}

// Every address on the sweep answers something; only an eSCL endpoint counts.
func TestProbeIgnoresAnythingElseOnTheNetwork(t *testing.T) {
	SetAllowLoopback(true)
	t.Cleanup(func() { SetAllowLoopback(false) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>router admin</body></html>"))
	}))
	t.Cleanup(server.Close)

	if _, ok := probe(context.Background(), newClient(), server.Listener.Addr().String()); ok {
		t.Fatal("a web server that is not a scanner was reported as one")
	}
}

func TestModelFromFallsBackWhenTheNameIsMissing(t *testing.T) {
	t.Parallel()

	model, ok := modelFrom(`<scan:ScannerCapabilities><pwg:Version>2.6</pwg:Version></scan:ScannerCapabilities>`)
	if !ok {
		t.Fatal("a capabilities document should be recognized without a model name")
	}
	if model != "eSCL scanner" {
		t.Fatalf("model=%q want the fallback", model)
	}
	if _, ok := modelFrom("not xml at all"); ok {
		t.Fatal("arbitrary content should not count as a scanner")
	}
}

func TestDefaultCIDRUsesTheBrowsersOwnNetworkAndTheUsualOnes(t *testing.T) {
	t.Parallel()

	if got := DefaultCIDR("10.1.2.44"); got != "10.1.2.0/24, 192.168.1.0/24, 192.168.0.0/24" {
		t.Fatalf("DefaultCIDR=%q", got)
	}
	// The browser is already on one of the defaults: offer it once, not twice.
	if got := DefaultCIDR("192.168.1.44"); got != "192.168.1.0/24, 192.168.0.0/24" {
		t.Fatalf("DefaultCIDR=%q", got)
	}
	// Behind a reverse proxy the address is the proxy's, and in a container it
	// is the bridge gateway -- neither says which LAN the user is on, so all
	// that is left is where a home network usually is.
	for _, address := range []string{"", "8.8.8.8", "::1", "not an address"} {
		if got := DefaultCIDR(address); got != "192.168.1.0/24, 192.168.0.0/24" {
			t.Fatalf("DefaultCIDR(%q)=%q want the defaults", address, got)
		}
	}
	// Whatever it offers has to be something it will accept back.
	if _, err := parseSweepPrefixes(DefaultCIDR("10.1.2.44")); err != nil {
		t.Fatalf("the default ranges were refused: %v", err)
	}
}

func TestParseSweepPrefixesBoundsTheWholeSweep(t *testing.T) {
	t.Parallel()

	got, err := parseSweepPrefixes(" 192.168.1.0/24, 192.168.1.0/24 10.0.0.0/24 ")
	if err != nil {
		t.Fatalf("parseSweepPrefixes() error: %v", err)
	}
	// The repeat is dropped rather than swept twice.
	if len(got) != 2 || got[0].String() != "192.168.1.0/24" || got[1].String() != "10.0.0.0/24" {
		t.Fatalf("prefixes=%v", got)
	}
	if got, err := parseSweepPrefixes("  "); err != nil || got != nil {
		t.Fatalf("an empty list should sweep nothing: %v %v", got, err)
	}
	// Each range is allowed on its own; together they are one probe too many.
	if _, err := parseSweepPrefixes("10.0.0.0/22, 192.168.1.0/24"); err == nil {
		t.Fatal("a list over the address budget should have been refused")
	}
}

func TestParseSweepPrefixRefusesWhatShouldNotBeSwept(t *testing.T) {
	t.Parallel()

	if _, err := parseSweepPrefix("192.168.1.0/24"); err != nil {
		t.Fatalf("a private /24 should be allowed: %v", err)
	}
	for _, cidr := range []string{
		"8.8.8.0/24",     // the sweep must not reach the internet
		"192.168.0.0/16", // 65k probes from one click
		"192.168.1.1",    // not a range
		"::1/128",        // IPv4 only
	} {
		if _, err := parseSweepPrefix(cidr); err == nil {
			t.Fatalf("%q should have been refused", cidr)
		}
	}
}

func TestLastAddrIsTheBroadcastAddress(t *testing.T) {
	t.Parallel()

	if got := lastAddr(netip.MustParsePrefix("192.168.1.0/24")); got.String() != "192.168.1.255" {
		t.Fatalf("lastAddr=%s", got)
	}
	if got := lastAddr(netip.MustParsePrefix("10.1.0.0/22")); got.String() != "10.1.3.255" {
		t.Fatalf("lastAddr=%s", got)
	}
}

func TestScannerFromEntryReadsTheTXTRecords(t *testing.T) {
	t.Parallel()

	scanner, ok := scannerFromEntry(&mdns.ServiceEntry{
		Name:       "Canon TR8500._uscan._tcp.local.",
		AddrV4:     net.ParseIP("192.168.1.9"),
		Port:       8080,
		InfoFields: []string{"txtvers=1", "rs=eSCL", "ty=Canon TR8500 series"},
	}, false)
	if !ok {
		t.Fatal("the entry should have been accepted")
	}
	if scanner.Model != "Canon TR8500 series" {
		t.Fatalf("model=%q", scanner.Model)
	}
	if scanner.URL != "http://192.168.1.9:8080/eSCL" {
		t.Fatalf("url=%q", scanner.URL)
	}
	if scanner.Source != "mdns" {
		t.Fatalf("source=%q", scanner.Source)
	}
}

// An advertisement is just a UDP packet anyone on the link can send, so the
// address in it goes through the same check as an address a user types.
func TestScannerFromEntryRefusesAnAddressOffTheLocalNetwork(t *testing.T) {
	t.Parallel()

	if _, ok := scannerFromEntry(&mdns.ServiceEntry{
		Name:   "evil._uscan._tcp.local.",
		AddrV4: net.ParseIP("8.8.8.8"),
		Port:   80,
	}, false); ok {
		t.Fatal("a public address should have been refused")
	}
}
