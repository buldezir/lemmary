// Package escl scans documents from a network scanner over eSCL, the
// driverless protocol Apple calls AirScan and most scanners sold since about
// 2015 speak out of the box.
//
// The whole protocol, as used here, is three requests: POST a ScanSettings
// document to {base}/ScanJobs and the scanner answers with a Location header
// naming a job; GET {job}/NextDocument and it answers with a PDF, once per
// sheet, until it has none left; DELETE {job} to let the device go. There is no
// discovery, authentication or capability negotiation in that path -- see
// discover.go for finding the device in the first place.
//
// Everything is fixed at A4, 300 dpi, colour, PDF. A scanner will happily do
// 1200 dpi, but a page of it is tens of megabytes and no better to read, and
// the documents.file field stops at 20 MB either way.
package escl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// defaultResource is the eSCL root nearly every device serves. mDNS can
	// name a different one, which is why the base URL carries the path.
	defaultResource = "eSCL"

	// A4 at 300 dpi, in the thirtieth-of-a-millimetre unit eSCL calls
	// ThreeHundredthsOfInches: 210mm x 297mm.
	a4Width    = 2480
	a4Height   = 3508
	resolution = 300

	// createTimeout bounds the job POST. The scanner only has to accept the
	// settings here; it does not scan yet.
	createTimeout = 30 * time.Second
	// pageTimeout bounds one NextDocument. This is where the scanner actually
	// moves the head, so it is the long one.
	pageTimeout = 3 * time.Minute
	// deleteTimeout bounds the job DELETE, which is best-effort anyway.
	deleteTimeout = 10 * time.Second

	// A device that is warming up, repositioning the head or picking up the
	// next sheet answers NextDocument with 503. It means "not yet", not "no",
	// and every other eSCL client retries it.
	notReadyRetries = 10

	// maxPages stops a feeder that keeps handing back pages -- a misfeed loop,
	// or a device that never reports the end -- from filling the disk.
	maxPages = 200
)

// notReadyDelay is how long to wait out a 503. A var so the tests do not.
var notReadyDelay = 2 * time.Second

// Source is where the scanner takes paper from.
type Source string

const (
	// Platen is the glass: one page per scan.
	Platen Source = "Platen"
	// Feeder is the automatic document feeder: every sheet in one job.
	Feeder Source = "Feeder"
)

// ParseSource maps the wire value, defaulting to the glass because every
// scanner has one.
func ParseSource(raw string) (Source, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "platen", "glass":
		return Platen, nil
	case "feeder", "adf":
		return Feeder, nil
	default:
		return "", fmt.Errorf("unknown scan source %q", raw)
	}
}

// scanSettings is the request body, as the issue's reference script sends it.
// A template rather than encoding/xml: the document is two namespaces deep with
// prefixes the scanners care about, and Go's marshaller spells those in a way
// several devices reject.
const scanSettings = `<?xml version="1.0" encoding="UTF-8"?>
<scan:ScanSettings xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03"
                   xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:Version>2.0</pwg:Version>
  <pwg:ScanRegions>
    <pwg:ScanRegion>
      <pwg:ContentRegionUnits>escl:ThreeHundredthsOfInches</pwg:ContentRegionUnits>
      <pwg:XOffset>0</pwg:XOffset>
      <pwg:YOffset>0</pwg:YOffset>
      <pwg:Width>%d</pwg:Width>
      <pwg:Height>%d</pwg:Height>
    </pwg:ScanRegion>
  </pwg:ScanRegions>
  <pwg:InputSource>%s</pwg:InputSource>
  <scan:ColorMode>RGB24</scan:ColorMode>
  <scan:XResolution>%d</scan:XResolution>
  <scan:YResolution>%d</scan:YResolution>
  <pwg:DocumentFormat>application/pdf</pwg:DocumentFormat>
  <scan:DocumentFormatExt>application/pdf</scan:DocumentFormatExt>
  <scan:Intent>Document</scan:Intent>
</scan:ScanSettings>
`

// Scan runs one scan job and returns the PDFs it produced, in order.
//
// The glass gives one. A feeder gives one per sheet -- or, on some devices, one
// PDF holding every sheet; both come back as a slice the caller merges, so the
// difference does not matter downstream.
//
// maxBytes caps the total; the scan stops and reports rather than reading an
// unbounded amount into memory.
func Scan(ctx context.Context, scanner string, source Source, maxBytes int64) ([][]byte, error) {
	base, err := normalizeBase(scanner)
	if err != nil {
		return nil, err
	}
	client := newClient()

	jobURL, err := createJob(ctx, client, base, source)
	if err != nil {
		return nil, err
	}
	// Best-effort, and deliberately not tied to ctx: a scanner that keeps a
	// finished job open refuses the next one, so it is worth telling the device
	// we are done even when the caller has given up.
	defer deleteJob(client, jobURL)

	var pages [][]byte
	var total int64
	for len(pages) < maxPages {
		page, done, err := nextDocument(ctx, client, jobURL, maxBytes-total)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		total += int64(len(page))
		if total > maxBytes {
			// maxBytes is what is left of the document's allowance, which is
			// not a number worth reporting -- "larger than 0 MB" is what the
			// last page of a full document would say.
			return nil, ErrTooLarge
		}
		pages = append(pages, page)
		if source == Platen {
			// The glass holds one sheet, and some devices answer a second
			// NextDocument by scanning it again.
			break
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("the scanner returned no pages (is there paper in the feeder?)")
	}
	return pages, nil
}

func createJob(ctx context.Context, client *http.Client, base string, source Source) (string, error) {
	body := fmt.Sprintf(scanSettings, a4Width, a4Height, source, resolution, resolution)

	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/ScanJobs", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "text/xml")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach the scanner: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusConflict, http.StatusServiceUnavailable:
		return "", fmt.Errorf("the scanner is busy with another job")
	default:
		return "", fmt.Errorf("the scanner refused the scan (HTTP %d)", resp.StatusCode)
	}

	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location == "" {
		return "", fmt.Errorf("the scanner accepted the job but did not say where it is")
	}
	return resolveJobURL(base, location)
}

// resolveJobURL turns the Location header into an absolute URL on the scanner.
// Devices send all three shapes: absolute, root-relative and bare.
//
// The host in an absolute one is not trusted, and not compared either: devices
// put their mDNS name, "localhost" or an explicit :80 in there, none of which
// match the address we dialed, and rejecting those rejects working scanners.
// Only the path is taken and the scheme and host we already reached are kept --
// which is what sane-airscan does, and is the stronger boundary besides: a
// Location naming another host cannot pull us off the scanner at all.
func resolveJobURL(base, location string) (string, error) {
	// Against the ScanJobs URL rather than the resource root, so a bare
	// "ScanJobs/id" resolves to {resource}/ScanJobs/id and not /ScanJobs/id.
	baseURL, err := url.Parse(base + "/ScanJobs")
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("the scanner returned an unusable job address")
	}
	job := baseURL.ResolveReference(ref)
	job.Scheme, job.Host, job.User = baseURL.Scheme, baseURL.Host, nil
	return strings.TrimRight(job.String(), "/"), nil
}

// nextDocument fetches one page. done is true once the scanner has no more.
//
// remaining is what is left of the caller's byte budget: the body is read
// through a limit rather than whole, so a device answering with something
// enormous cannot put it all on the heap before the caller notices.
func nextDocument(ctx context.Context, client *http.Client, jobURL string, remaining int64) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, pageTimeout)
	defer cancel()

	if remaining < 0 {
		remaining = 0
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, jobURL+"/NextDocument", nil)
		if err != nil {
			return nil, false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, false, fmt.Errorf("the scan failed: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound, http.StatusGone, http.StatusNoContent:
			// The documented end of a job: the feeder is empty.
			resp.Body.Close()
			return nil, true, nil
		case http.StatusServiceUnavailable:
			resp.Body.Close()
			if attempt == notReadyRetries {
				return nil, false, fmt.Errorf("the scanner was not ready in time")
			}
			select {
			case <-time.After(notReadyDelay):
				continue
			case <-ctx.Done():
				return nil, false, fmt.Errorf("the scan failed: %w", ctx.Err())
			}
		default:
			resp.Body.Close()
			return nil, false, fmt.Errorf("the scan failed (HTTP %d)", resp.StatusCode)
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, remaining+1))
		resp.Body.Close()
		if err != nil {
			return nil, false, fmt.Errorf("the scan failed: %w", err)
		}
		if len(data) == 0 {
			return nil, true, nil
		}
		return data, false, nil
	}
}

func deleteJob(client *http.Client, jobURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, jobURL, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
}
