# Scanning from a network scanner

Upload → **Scan** scans straight from a scanner on your local network. No
driver, no scanning software, no computer in between: Lemmary talks to the
device itself, over the protocol Apple calls AirScan and the standards call
eSCL. Most scanners and multifunction printers sold since roughly 2015 speak it
out of the box — Brother, Canon, Epson, HP, Lexmark, Ricoh, Xerox — usually
under a setting named "AirPrint", "Mopria" or "driverless".

Pages are scanned at A4, 300 dpi, colour. Scan as many as the document has,
each one appended to the end, then add the whole thing to your library as a
single document, which is then OCRed and read like any other upload.

## Pointing Lemmary at the scanner

Press **Find scanners**. Anything that answers appears in a list; pick it and
the address fills in. The browser remembers the last one you used.

If nothing turns up, type the scanner's address in the field — `192.168.1.9`
is enough. The address is on the device's own network settings page, or in your
router's list of connected devices.

Only private addresses are accepted: `10.x`, `172.16–31.x` and `192.168.x`.
Lemmary will not scan from a public address, from `localhost`, or from a
link-local `169.254.x` one — the last of those is where cloud providers keep
their metadata service, and an app that fetches whatever address it is given is
an app that can be pointed at it.

## How the two searches work, and when each fails

**Find scanners** runs two searches at once.

**mDNS** is the protocol's own answer. Scanners advertise themselves on the
local link as `_uscan._tcp`, and a browse gets the model name, the port and the
right URL path from the device itself. It needs multicast traffic to reach
Lemmary, which it does when the app runs directly on the host — but **not**
through Docker's default bridge network, where nothing on the LAN is reachable
by multicast at all. Under `docker compose up` as shipped, mDNS finds nothing.

To let mDNS through, put the container on the host's own network:

```yaml
services:
  app:
    image: ghcr.io/buldezir/lemmary:latest
    network_mode: host   # gives the container the host's LAN, mDNS included
    env_file: [.env]
    volumes: [app_data:/app/pb_data]
```

`network_mode: host` replaces the `ports:` mapping — the app then listens on
`PORT` (80 by default; set `PORT=8090` in `.env` to keep the usual address).
It only works on Linux: Docker Desktop on macOS and Windows runs containers
inside a virtual machine that has no path to the LAN's multicast traffic.

**The sweep** is what works everywhere else. It asks every address in one
network range whether it has an eSCL endpoint, which is an ordinary HTTP
request and routes out of a bridge network like any other. A /24 takes about
two seconds.

Three ranges are swept by default: the one your browser connected from, and
`192.168.1.0/24` and `192.168.0.0/24`, where a home network almost always is.
The first is only a guess — a container sees the bridge gateway rather than
your address, and behind a reverse proxy it sees the proxy — which is why the
usual two are swept regardless.

If your network is somewhere else, type its range into **Ranges** and search
again. Several ranges, separated by commas, are swept together. A range larger
than a /22, or a list adding up to more than 1024 addresses, is refused:
sweeping 65,000 addresses from a button press is not something to do by
accident.

A scanner on a different subnet from the app is invisible to mDNS but findable
by sweeping its range.

## Glass or feeder

**Glass** scans the one page on the platen. Press **Scan another page** for
each further sheet.

**Feeder** pulls every sheet in the tray in a single job, which is the one to
use for a stack. Some devices return one PDF per sheet and some return one PDF
for the lot; either way it arrives as one document.

## Limits

A document may be at most 20 MB, which is the same ceiling as any upload, and
at 300 dpi colour it works out at somewhere between ten and twenty pages of
ordinary paper. When the scan reaches it, add what you have to your library and
start a second document.

A half-finished scan is kept for 30 minutes after the last page you scanned. After that it is discarded and the
pages have to be scanned again — the file lives in the same place as any other
staged upload, so it is covered by [encryption at rest](/encryption) when that
is turned on.

## When it does not work

**"Could not reach the scanner"** — the device is asleep, on another subnet, or
not speaking eSCL. Check that it answers at
`http://<address>/eSCL/ScannerCapabilities` from a machine on the same network:
a page of XML means the protocol is on.

**"The scanner is busy with another job"** — something else is scanning, or a
previous job was never closed. Most devices clear it after a minute; the stubborn
ones want a power cycle.

**"The scanner returned no pages"** — the feeder is empty, or the sheet was not
picked up.
