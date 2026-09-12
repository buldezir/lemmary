import { pb, pbUrl } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch, pollJob } from '../apiClient'

/** Where the scanner takes paper from. */
export type ScanSource = 'platen' | 'feeder'

export type FoundScanner = {
  model: string
  host: string
  url: string
  /** Which method found it: mDNS carries the model, the sweep guesses port 80. */
  source: 'mdns' | 'sweep'
}

export type ScanDiscovery = {
  scanners: FoundScanner[]
  /** The range that was swept, so the field can show what was actually tried. */
  cidr: string
}

/** The document scanned so far. */
export type StagedScan = {
  upload_id: string
  page_count: number
  size_bytes: number
  expires_at: string
}

/**
 * A scan is minutes of real work: the head moves, and a feeder runs a stack of
 * paper through. The poll budget has to cover that, or the page reports a
 * failure while the scanner is still going — the backend allows three minutes
 * per sheet, and a feeder load is as many sheets as fit in 20 MB.
 *
 * Polling ends the moment the job does, so this only bounds a scanner that has
 * genuinely stopped answering.
 */
const scanTimeoutMs = 45 * 60 * 1000

/** The scanner the browser used last, so the picker starts where it left off. */
const lastScannerKey = 'lemmary.scan.scanner'

export function rememberScanner(url: string) {
  try {
    if (url) localStorage.setItem(lastScannerKey, url)
    else localStorage.removeItem(lastScannerKey)
  } catch {
    // A browser with storage blocked just forgets between visits.
  }
}

export function recallScanner(): string {
  try {
    return localStorage.getItem(lastScannerKey) ?? ''
  } catch {
    return ''
  }
}

/**
 * Looks for scanners, by mDNS and by sweeping a network range.
 *
 * An omitted range makes the server suggest one from the address the browser
 * reached it from, and report back which it used.
 */
export function discoverScanners(cidr?: string) {
  const query = cidr ? `?cidr=${encodeURIComponent(cidr)}` : ''
  return apiFetch<ScanDiscovery>(`/api/app/scan/discover${query}`, {
    fallbackError: 'Could not look for scanners',
  })
}

/**
 * Scans one page, or a whole feeder load, onto the end of the document.
 *
 * An omitted uploadId starts a new document; the id to pass next time comes
 * back in the result.
 */
export async function scanPage(
  scanner: string,
  source: ScanSource,
  uploadId?: string,
): Promise<StagedScan> {
  const start = await apiFetch<{ job_id?: string }>('/api/app/scan', {
    method: 'POST',
    body: { scanner, source, upload_id: uploadId ?? '' },
    fallbackError: 'The scan failed to start',
  })
  if (!start.job_id) {
    throw new Error('Scan job id missing from server response')
  }
  return pollJob<StagedScan>(`/api/app/scan/status?job_id=${encodeURIComponent(start.job_id)}`, {
    label: 'scan',
    timeoutMs: scanTimeoutMs,
  })
}

/** Throws away a scan the user did not keep. */
export async function discardScan(uploadId: string) {
  await apiFetch<unknown>(`/api/app/scan?upload_id=${encodeURIComponent(uploadId)}`, {
    method: 'DELETE',
    fallbackError: 'Failed to discard the scan',
  })
}

/** Adds the scanned pages to the library as one document. */
export function saveScan(uploadId: string) {
  return apiFetch<{ document_id: string }>('/api/app/scan/document', {
    method: 'POST',
    body: { upload_id: uploadId },
    fallbackError: 'Failed to save the scan',
  })
}

/**
 * Loads the scanned document as an object URL for the preview. The endpoint
 * needs the session token, which an `<object data>` cannot carry, so the PDF is
 * fetched and wrapped in a blob URL instead. Callers must revoke it when done.
 */
export async function fetchScanPdf(uploadId: string): Promise<string> {
  await ensureAuth()
  const path = `/api/app/scan/pdf?upload_id=${encodeURIComponent(uploadId)}`
  const response = await fetch(`${pbUrl}${path}`, {
    headers: { Authorization: pb.authStore.token },
  })
  if (!response.ok) {
    throw new Error('Failed to load the scan')
  }
  return URL.createObjectURL(await response.blob())
}

/** "3 pages · 1.2 MB", under the preview. */
export function describeScan(scan: StagedScan): string {
  const pages = scan.page_count === 1 ? '1 page' : `${scan.page_count} pages`
  const mb = scan.size_bytes / (1024 * 1024)
  const size = mb < 0.1 ? `${Math.round(scan.size_bytes / 1024)} KB` : `${mb.toFixed(1)} MB`
  return `${pages} · ${size}`
}
