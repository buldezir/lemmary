import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'

import {
  describeScan,
  discardScan,
  discoverScanners,
  fetchScanPdf,
  recallScanner,
  rememberScanner,
  saveScan,
  scanPage,
  type FoundScanner,
  type ScanSource,
  type StagedScan,
} from '../lib/api/scan'
import { documentsLanding } from '../lib/reviewPolicy'
import { Button, inputClassName, labelTextClassName } from '../components/ui'

function errorMessage(err: unknown, fallback: string) {
  return err instanceof Error && err.message ? err.message : fallback
}

/**
 * Loads the scanned document as a blob URL for the preview, and revokes it when
 * the scan changes or the page goes away.
 */
function useScanPreview(uploadId: string | undefined, pageCount: number) {
  // Keyed by the scan *and* its page count: the upload id does not change when
  // a page is appended, but the document behind it does. Keying it also means a
  // stale URL is simply not returned, rather than needing a reset in the effect.
  const key = uploadId ? `${uploadId}:${pageCount}` : ''
  const [loaded, setLoaded] = useState({ key: '', url: '' })

  useEffect(() => {
    if (!uploadId || !key) return

    let cancelled = false
    let objectUrl = ''

    fetchScanPdf(uploadId)
      .then((next) => {
        if (cancelled) {
          URL.revokeObjectURL(next)
          return
        }
        objectUrl = next
        setLoaded({ key, url: next })
      })
      .catch(() => {
        // The preview is a convenience; the pages are staged either way.
      })

    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [uploadId, key])

  return loaded.key === key ? loaded.url : ''
}

export function UploadScanPage() {
  const [scanner, setScanner] = useState(recallScanner)
  const [source, setSource] = useState<ScanSource>('platen')
  const [found, setFound] = useState<FoundScanner[] | null>(null)
  const [searchedCidr, setSearchedCidr] = useState('')
  const [cidr, setCidr] = useState('')
  const [searching, setSearching] = useState(false)
  const [scanning, setScanning] = useState(false)
  const [saving, setSaving] = useState(false)
  const [scan, setScan] = useState<StagedScan | null>(null)
  const [savedId, setSavedId] = useState('')
  const [error, setError] = useState('')

  const previewUrl = useScanPreview(scan?.upload_id, scan?.page_count ?? 0)
  // Saving claims the staged scan, so scanning or saving again while it is in
  // flight loses the race and reports it as an expiry.
  const busy = searching || scanning || saving

  async function onFind(range?: string) {
    try {
      setSearching(true)
      setError('')
      const result = await discoverScanners(range)
      setFound(result.scanners)
      setSearchedCidr(result.cidr)
      setCidr(result.cidr)
      // One scanner and an empty field is not a choice worth making.
      if (result.scanners.length === 1 && !scanner) {
        pick(result.scanners[0])
      }
    } catch (err) {
      setFound([])
      setError(errorMessage(err, 'Could not look for scanners'))
    } finally {
      setSearching(false)
    }
  }

  function pick(device: FoundScanner) {
    setScanner(device.url)
    rememberScanner(device.url)
  }

  async function onScan() {
    if (!scanner.trim()) {
      setError('Enter the scanner address, or search for one.')
      return
    }
    try {
      setScanning(true)
      setError('')
      setSavedId('')
      rememberScanner(scanner.trim())
      setScan(await scanPage(scanner.trim(), source, scan?.upload_id))
    } catch (err) {
      setError(errorMessage(err, 'The scan failed'))
    } finally {
      setScanning(false)
    }
  }

  async function onSave() {
    if (!scan) return
    try {
      setSaving(true)
      setError('')
      const { document_id } = await saveScan(scan.upload_id)
      setScan(null)
      setSavedId(document_id)
    } catch (err) {
      setError(errorMessage(err, 'Failed to save the scan'))
    } finally {
      setSaving(false)
    }
  }

  async function onDiscard() {
    if (!scan) return
    const uploadId = scan.upload_id
    setScan(null)
    setError('')
    try {
      await discardScan(uploadId)
    } catch {
      // Best-effort cleanup: a staged scan expires on its own.
    }
  }

  return (
    <section className="flex flex-col gap-5">
      <div>
        <h2 className="font-display text-xl font-semibold text-ink">Scan</h2>
        <p className="mt-1 text-sm text-ink-soft">
          Scan straight from a network scanner on your local network — no driver, no computer in
          between. Scan as many pages as the document has, then add them as one document.{' '}
          <a
            href="/docs/scanning"
            target="_blank"
            rel="noopener noreferrer"
            className="underline hover:text-oxblood"
          >
            How this finds your scanner
          </a>
        </p>
      </div>

      <div className="flex flex-col gap-4 rounded-none border border-line bg-surface p-5">
        <div className="flex flex-wrap items-end gap-3">
          <label className="flex min-w-64 flex-1 flex-col gap-1">
            <span className={labelTextClassName}>Scanner</span>
            <input
              className={inputClassName}
              value={scanner}
              placeholder="192.168.1.9"
              disabled={busy}
              onChange={(event) => setScanner(event.target.value)}
            />
          </label>
          <Button variant="secondary" disabled={busy} onClick={() => void onFind()}>
            {searching ? 'Searching…' : 'Find scanners'}
          </Button>
        </div>

        {found !== null && found.length > 0 && (
          <ul className="flex flex-col gap-1">
            {found.map((device) => (
              <li key={device.url}>
                <button
                  type="button"
                  onClick={() => pick(device)}
                  aria-pressed={scanner === device.url}
                  className={`flex w-full items-baseline justify-between gap-3 border px-3 py-2 text-left text-sm transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood ${
                    scanner === device.url
                      ? 'border-oxblood bg-bright text-ink'
                      : 'border-line bg-bright text-ink hover:border-ink'
                  }`}
                >
                  <span className="font-medium">{device.model}</span>
                  <span className="text-xs text-ink-soft">{device.host}</span>
                </button>
              </li>
            ))}
          </ul>
        )}

        {found !== null && (
          <div className="flex flex-wrap items-end gap-3">
            <p className="text-xs text-ink-soft">
              {found.length === 0 ? 'Found nothing' : `Found ${found.length}`}
              {searchedCidr ? ` on ${searchedCidr}` : ''} and over mDNS.
              {found.length === 0 && ' Try another range, or type the address above.'}
            </p>
            <label className="flex flex-col gap-1">
              <span className={labelTextClassName}>Ranges</span>
              <input
                className={`${inputClassName} w-72`}
                value={cidr}
                placeholder="192.168.1.0/24, 10.0.0.0/24"
                disabled={busy}
                onChange={(event) => setCidr(event.target.value)}
              />
            </label>
            <Button variant="secondary" disabled={busy} onClick={() => void onFind(cidr)}>
              Search again
            </Button>
          </div>
        )}

        <fieldset className="flex flex-wrap items-center gap-3">
          <legend className={`${labelTextClassName} mb-1`}>Source</legend>
          {(
            [
              ['platen', 'Glass', 'One page at a time'],
              ['feeder', 'Feeder', 'Every sheet in the tray'],
            ] as const
          ).map(([value, label, hint]) => (
            <label
              key={value}
              className={`flex cursor-pointer items-baseline gap-2 border px-3 py-2 text-sm transition-colors ${
                source === value ? 'border-oxblood bg-bright' : 'border-line bg-bright hover:border-ink'
              }`}
            >
              <input
                type="radio"
                name="scan-source"
                value={value}
                checked={source === value}
                disabled={busy}
                onChange={() => setSource(value)}
                className="accent-oxblood"
              />
              <span className="font-medium text-ink">{label}</span>
              <span className="text-xs text-ink-soft">{hint}</span>
            </label>
          ))}
        </fieldset>

        <div>
          <Button disabled={busy} onClick={() => void onScan()}>
            {scanning ? 'Scanning…' : scan ? 'Scan another page' : 'Scan'}
          </Button>
        </div>
      </div>

      {scanning && (
        <div className="rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">Scanning…</p>
          <p className="mt-1 text-sm text-ink-soft">
            {source === 'feeder'
              ? 'Pulling every sheet in the tray. This takes a while.'
              : 'A page at 300 dpi takes half a minute or so.'}
          </p>
        </div>
      )}

      {scan && (
        <div className="flex flex-col gap-4 rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">{describeScan(scan)} scanned so far.</p>
          {previewUrl && (
            <object
              data={previewUrl}
              type="application/pdf"
              aria-label="The scanned document so far"
              className="h-[32rem] w-full border border-line bg-surface"
            />
          )}
          <div className="flex flex-wrap gap-2">
            <Button disabled={busy} onClick={() => void onSave()}>
              {saving ? 'Adding…' : 'Add to your library'}
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => void onDiscard()}>
              Discard
            </Button>
          </div>
        </div>
      )}

      {savedId && (
        <div className="flex flex-col gap-3 rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            Added to your library, and queued for OCR and AI processing.
          </p>
          <Link to={documentsLanding()} className="text-sm font-medium text-oxblood underline">
            {documentsLanding() === '/inbox' ? 'Open the Inbox' : 'Open documents'}
          </Link>
        </div>
      )}

      {error && <p className="text-sm text-madder">{error}</p>}
    </section>
  )
}
