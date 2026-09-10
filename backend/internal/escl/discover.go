package escl

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

// Two ways to find a scanner, because neither works everywhere.
//
// mDNS is the protocol's own answer: eSCL devices advertise _uscan._tcp, and a
// browse gets the model name, the port and the resource path from the device
// itself. It only works if multicast reaches the app -- which it does not
// through Docker's default bridge network, where nothing is listening on
// 224.0.0.251. See docs/scanning.md.
//
// The sweep is what works there: an ordinary unicast GET of
// /eSCL/ScannerCapabilities on every address of one /24, which routes out of a
// bridge network like any other request. It is also the only way to find a
// scanner on a subnet mDNS does not cross.
//
// So both run, concurrently, and the results are merged.
const (
	// probeTimeout is how long one address gets to answer. A scanner on the
	// same LAN answers in single-digit milliseconds; this is generous, and it
	// is what makes a /24 finish in about two seconds.
	probeTimeout = 600 * time.Millisecond
	// probeWorkers is how many addresses are in flight at once.
	probeWorkers = 64
	// browseTimeout is how long the mDNS query listens. Devices answer in well
	// under a second; the rest is for a slow or lossy wireless link.
	browseTimeout = 2 * time.Second
	// minPrefixBits bounds a sweep at 1024 addresses, so a mistyped CIDR costs
	// a couple of seconds rather than scanning a corporate network.
	minPrefixBits = 22
)

// Scanner is one device that answered.
type Scanner struct {
	// Model is what the device calls itself, for the picker.
	Model string `json:"model"`
	// Host is the address to show, without the eSCL path.
	Host string `json:"host"`
	// URL is the base to hand back to Scan.
	URL string `json:"url"`
	// Source says which of the two methods found it, so the UI can explain a
	// device that mDNS knows about but the sweep cannot see, or the reverse.
	Source string `json:"source"`
}

var makeAndModel = regexp.MustCompile(`<pwg:MakeAndModel>([^<]+)</pwg:MakeAndModel>`)

// Discover looks for scanners, by mDNS and by sweeping cidr, and returns what
// either method found. An empty cidr skips the sweep.
//
// A CIDR that is not private, or is bigger than a /22, is refused: this makes
// the server issue requests on the caller's behalf, and a typo must not turn it
// into a port scanner.
func Discover(ctx context.Context, cidr string) ([]Scanner, error) {
	var prefix netip.Prefix
	sweeping := strings.TrimSpace(cidr) != ""
	if sweeping {
		var err error
		prefix, err = parseSweepPrefix(cidr)
		if err != nil {
			return nil, err
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	found := map[string]Scanner{}
	collect := func(scanners []Scanner) {
		mu.Lock()
		defer mu.Unlock()
		for _, scanner := range scanners {
			// Keyed on the address alone: mDNS spells a non-standard port into
			// Host and the sweep never does, so one device would otherwise be
			// offered twice, once at a port that does not answer.
			key := scanner.Host
			if host, _, err := net.SplitHostPort(key); err == nil {
				key = host
			}
			// mDNS wins a tie: it carries the model and the resource path from
			// the device itself, where the sweep only guesses at port 80.
			if existing, ok := found[key]; ok && existing.Source == "mdns" {
				continue
			}
			found[key] = scanner
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		collect(browse(ctx))
	}()
	if sweeping {
		wg.Add(1)
		go func() {
			defer wg.Done()
			collect(sweep(ctx, prefix))
		}()
	}
	wg.Wait()

	scanners := make([]Scanner, 0, len(found))
	for _, scanner := range found {
		scanners = append(scanners, scanner)
	}
	sort.Slice(scanners, func(i, j int) bool { return scanners[i].Host < scanners[j].Host })
	return scanners, nil
}

// DefaultCIDR is the /24 to offer as the sweep range, taken from the address
// the browser reached us from: the app runs on a container network of its own
// and has no other way to know which LAN the user is on.
//
// Empty when that address is not a private IPv4 -- behind a reverse proxy it is
// the proxy's, and PocketBase's TrustedProxy is not configured here. The user
// then fills the field in, and mDNS may answer on its own regardless.
func DefaultCIDR(clientIP string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(clientIP))
	if err != nil || !addr.Is4() || !addr.IsPrivate() {
		return ""
	}
	return netip.PrefixFrom(addr, 24).Masked().String()
}

func parseSweepPrefix(cidr string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not a network range like 192.168.1.0/24", cidr)
	}
	prefix = prefix.Masked()
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("only IPv4 ranges can be swept")
	}
	if !prefix.Addr().IsPrivate() {
		return netip.Prefix{}, fmt.Errorf("%s is not a private network range", prefix)
	}
	if prefix.Bits() < minPrefixBits {
		return netip.Prefix{}, fmt.Errorf("%s is too large to sweep; use a /%d or smaller", prefix, minPrefixBits)
	}
	return prefix, nil
}

// sweep probes every usable address in prefix for an eSCL endpoint.
func sweep(ctx context.Context, prefix netip.Prefix) []Scanner {
	client := newClient()

	addresses := make(chan netip.Addr)
	go func() {
		defer close(addresses)
		last := lastAddr(prefix)
		for addr := prefix.Addr(); prefix.Contains(addr); addr = addr.Next() {
			// The network and broadcast addresses of the range are not hosts.
			if prefix.Bits() < 31 && (addr == prefix.Addr() || addr == last) {
				continue
			}
			select {
			case addresses <- addr:
			case <-ctx.Done():
				return
			}
		}
	}()

	var mu sync.Mutex
	var found []Scanner
	var wg sync.WaitGroup
	for range probeWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for addr := range addresses {
				model, ok := probe(ctx, client, addr.String())
				if !ok {
					continue
				}
				mu.Lock()
				found = append(found, Scanner{
					Model:  model,
					Host:   addr.String(),
					URL:    "http://" + addr.String() + "/" + defaultResource,
					Source: "sweep",
				})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return found
}

// probe asks one address for its scanner capabilities, returning the model name
// if it is an eSCL device.
func probe(ctx context.Context, client *http.Client, host string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	url := "http://" + host + "/" + defaultResource + "/ScannerCapabilities"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	// The model sits near the top of the document, so the first few KB is
	// enough and a device serving something enormous cannot tie up a worker.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", false
	}
	return modelFrom(string(body))
}

// modelFrom pulls the model name out of a ScannerCapabilities document, and
// reports whether the document is one at all.
func modelFrom(body string) (string, bool) {
	if !strings.Contains(body, "ScannerCapabilities") && !strings.Contains(body, "MakeAndModel") {
		return "", false
	}
	if match := makeAndModel.FindStringSubmatch(body); match != nil {
		return strings.TrimSpace(match[1]), true
	}
	return "eSCL scanner", true
}

// browse asks the local link for eSCL services over mDNS.
func browse(ctx context.Context) []Scanner {
	var mu sync.Mutex
	var found []Scanner

	for _, service := range []string{"_uscan._tcp", "_uscans._tcp"} {
		entries := make(chan *mdns.ServiceEntry, 16)
		var drained sync.WaitGroup
		drained.Add(1)
		go func() {
			defer drained.Done()
			for entry := range entries {
				scanner, ok := scannerFromEntry(entry, service == "_uscans._tcp")
				if !ok {
					continue
				}
				mu.Lock()
				found = append(found, scanner)
				mu.Unlock()
			}
		}()

		// Errors are not reported: no multicast route is the normal case inside
		// a bridge-networked container, and it is not something the user asked
		// about or can act on from here. The sweep is the answer there.
		//nolint:errcheck
		mdns.QueryContext(ctx, &mdns.QueryParam{
			Service:     service,
			Domain:      "local",
			Timeout:     browseTimeout,
			Entries:     entries,
			DisableIPv6: true,
		})
		close(entries)
		drained.Wait()
	}
	return found
}

func scannerFromEntry(entry *mdns.ServiceEntry, secure bool) (Scanner, bool) {
	if entry == nil || entry.AddrV4 == nil {
		return Scanner{}, false
	}
	addr, ok := netip.AddrFromSlice(entry.AddrV4.To4())
	if !ok || !allowedScanIP(entry.AddrV4) {
		return Scanner{}, false
	}

	scheme := "http"
	if secure {
		scheme = "https"
	}
	host := addr.String()
	if entry.Port != 0 && entry.Port != 80 && entry.Port != 443 {
		host = net.JoinHostPort(addr.String(), fmt.Sprint(entry.Port))
	}

	// TXT records: rs is the resource path the eSCL endpoints hang off, ty the
	// model as the device would like it written.
	resource := defaultResource
	model := strings.TrimSpace(entry.Name)
	for _, field := range entry.InfoFields {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "rs":
			if trimmed := strings.Trim(strings.TrimSpace(value), "/"); trimmed != "" {
				resource = trimmed
			}
		case "ty":
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				model = trimmed
			}
		}
	}
	if model == "" {
		model = "eSCL scanner"
	}

	return Scanner{
		Model:  model,
		Host:   host,
		URL:    scheme + "://" + host + "/" + resource,
		Source: "mdns",
	}, true
}

// lastAddr is the broadcast address of a prefix.
func lastAddr(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Addr().As4()
	host := uint32(32 - prefix.Bits())
	if host >= 32 {
		return prefix.Addr()
	}
	value := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	value |= (uint32(1) << host) - 1
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}
