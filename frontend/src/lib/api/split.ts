import { pb, pbUrl } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch, pollJob, type JobProgress } from '../apiClient'

/** A sub-document, as inclusive 1-based page numbers. */
export type SplitPart = {
  from: number
  to: number
}

export type SuggestedSplitPart = SplitPart & {
  title?: string
}

export type SplitUpload = {
  upload_id: string
  file_name: string
  page_count: number
  size_bytes: number
  expires_at: string
}

export type SplitSuggestion = {
  parts: SuggestedSplitPart[]
  /** "pdf" when the page text came from the file, "ocr" when it was read by OCR. */
  text_source: 'pdf' | 'ocr'
}

export type SplitResult = {
  created: number
  skipped_duplicates: number
  skipped_oversized: number
  failed: number
  errors: string[]
  document_ids: string[]
}

export type SplitProgress = JobProgress

/**
 * Poll budgets sized to what the backend may legitimately spend, so the UI does
 * not report a failure while the server is still working.
 *
 * Detection reads up to 40 pages of a scan through the OCR provider before it
 * even calls the model; a split extracts up to 100 parts, each with a minute of
 * poppler budget, and then saves them.
 */
const detectTimeoutMs = 30 * 60 * 1000
const splitTimeoutMs = 2 * 60 * 60 * 1000

/** Stages a multi-document PDF and reports how many pages it holds. */
export function uploadSplitPdf(file: File) {
  const formData = new FormData()
  formData.append('file', file)

  return apiFetch<SplitUpload>('/api/app/split/upload', {
    method: 'POST',
    formData,
    fallbackError: 'Failed to read the PDF',
  })
}

/** Drops a staged PDF the user chose not to split. */
export async function discardSplitUpload(uploadId: string) {
  await apiFetch<unknown>(`/api/app/split/upload?upload_id=${encodeURIComponent(uploadId)}`, {
    method: 'DELETE',
    fallbackError: 'Failed to discard the PDF',
  })
}

/**
 * Loads the thumbnail of one page as an object URL. The endpoint needs the
 * session token, which an `<img src>` cannot carry, so the PNG is fetched and
 * wrapped in a blob URL instead. Callers must revoke it when done.
 */
export async function fetchPageThumb(uploadId: string, page: number): Promise<string> {
  await ensureAuth()
  const path = `/api/app/split/page?upload_id=${encodeURIComponent(uploadId)}&page=${page}`
  const response = await fetch(`${pbUrl}${path}`, {
    headers: { Authorization: pb.authStore.token },
  })
  if (!response.ok) {
    throw new Error(`Failed to load page ${page}`)
  }
  return URL.createObjectURL(await response.blob())
}

/** Asks the extraction model where the documents start, reporting progress. */
export async function detectSplitParts(
  uploadId: string,
  onProgress?: (progress: SplitProgress) => void,
): Promise<SplitSuggestion> {
  const start = await apiFetch<{ job_id?: string }>('/api/app/split/detect', {
    method: 'POST',
    body: { upload_id: uploadId },
    fallbackError: 'Detection failed to start',
  })
  if (!start.job_id) {
    throw new Error('Detection job id missing from server response')
  }

  const result = await pollJob<SplitSuggestion>(
    `/api/app/split/detect/status?job_id=${encodeURIComponent(start.job_id)}`,
    { onProgress, label: 'detection', timeoutMs: detectTimeoutMs },
  )
  return { ...result, parts: result.parts ?? [] }
}

/** Splits the staged PDF into one document per part, reporting progress. */
export async function runSplit(
  uploadId: string,
  parts: SplitPart[],
  onProgress?: (progress: SplitProgress) => void,
): Promise<SplitResult> {
  const start = await apiFetch<{ job_id?: string }>('/api/app/split', {
    method: 'POST',
    body: { upload_id: uploadId, parts },
    fallbackError: 'Split failed to start',
  })
  if (!start.job_id) {
    throw new Error('Split job id missing from server response')
  }

  const result = await pollJob<SplitResult>(
    `/api/app/split/status?job_id=${encodeURIComponent(start.job_id)}`,
    { onProgress, label: 'split', timeoutMs: splitTimeoutMs },
  )
  return { ...result, errors: result.errors ?? [], document_ids: result.document_ids ?? [] }
}

/**
 * Turns "cut after page n" markers into the exact page cover the API requires.
 */
export function partsFromCuts(pageCount: number, cuts: Iterable<number>): SplitPart[] {
  const boundaries = [...new Set(cuts)]
    .filter((cut) => cut >= 1 && cut < pageCount)
    .sort((a, b) => a - b)

  const parts: SplitPart[] = []
  let from = 1
  for (const to of [...boundaries, pageCount]) {
    parts.push({ from, to })
    from = to + 1
  }
  return parts
}

/** Turns detected parts back into the cut markers the page grid renders. */
export function cutsFromParts(parts: SplitPart[], pageCount: number): Set<number> {
  const cuts = new Set<number>()
  for (const part of parts) {
    if (part.to >= 1 && part.to < pageCount) cuts.add(part.to)
  }
  return cuts
}

/** "pages 1, 2–3, 4–12" — how the parts read in the confirmation summary. */
export function describeParts(parts: SplitPart[]): string {
  return parts.map((part) => (part.from === part.to ? `${part.from}` : `${part.from}–${part.to}`)).join(', ')
}
