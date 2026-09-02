import { type DragEvent, useCallback, useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  cutsFromParts,
  describeParts,
  detectSplitParts,
  discardSplitUpload,
  fetchPageThumb,
  partsFromCuts,
  runSplit,
  uploadSplitPdf,
  type SplitProgress,
  type SplitResult,
  type SplitUpload,
} from '../lib/api/split'
import { Button } from '../components/ui'

const ACCEPT_ATTR = '.pdf,application/pdf'

/** How many thumbnails are fetched at once, so a long scan still paints early. */
const THUMB_CONCURRENCY = 4

function plural(count: number, word: string) {
  return `${count} ${word}${count === 1 ? '' : 's'}`
}

function isPdf(file: File) {
  return file.name.toLowerCase().endsWith('.pdf') || file.type === 'application/pdf'
}

function errorMessage(err: unknown, fallback: string) {
  return err instanceof Error && err.message ? err.message : fallback
}

/**
 * Loads every page thumbnail as an object URL, a few at a time, and keeps them
 * revoked on unmount so a long scan does not leak blobs.
 */
function usePageThumbs(uploadId: string | undefined, pageCount: number) {
  // Keyed by upload so a stale map is simply ignored when the upload changes,
  // rather than needing a synchronous reset inside the effect.
  const [loaded, setLoaded] = useState<{ uploadId: string; pages: Record<number, string> }>({
    uploadId: '',
    pages: {},
  })

  useEffect(() => {
    if (!uploadId || pageCount < 1) return

    let cancelled = false
    const urls: string[] = []

    const queue = Array.from({ length: pageCount }, (_, i) => i + 1)
    const worker = async () => {
      for (;;) {
        const page = queue.shift()
        if (page === undefined || cancelled) return
        try {
          const url = await fetchPageThumb(uploadId, page)
          if (cancelled) {
            URL.revokeObjectURL(url)
            return
          }
          urls.push(url)
          setLoaded((current) =>
            current.uploadId === uploadId
              ? { uploadId, pages: { ...current.pages, [page]: url } }
              : { uploadId, pages: { [page]: url } },
          )
        } catch {
          // A thumbnail is a convenience: the page still counts without it.
        }
      }
    }
    void Promise.all(Array.from({ length: THUMB_CONCURRENCY }, worker))

    return () => {
      cancelled = true
      for (const url of urls) URL.revokeObjectURL(url)
    }
  }, [uploadId, pageCount])

  return loaded.uploadId === uploadId ? loaded.pages : {}
}

export function UploadSplitPage() {
  const inputRef = useRef<HTMLInputElement>(null)
  const [reading, setReading] = useState(false)
  const [upload, setUpload] = useState<SplitUpload | null>(null)
  const [cuts, setCuts] = useState<Set<number>>(new Set())
  const [dragging, setDragging] = useState(false)
  const [detecting, setDetecting] = useState<SplitProgress | null>(null)
  const [splitting, setSplitting] = useState<SplitProgress | null>(null)
  const [result, setResult] = useState<SplitResult | null>(null)
  const [zoomPage, setZoomPage] = useState<number | null>(null)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')

  const pageCount = upload?.page_count ?? 0
  const thumbs = usePageThumbs(upload?.upload_id, pageCount)
  const parts = partsFromCuts(pageCount, cuts)
  const busy = detecting !== null || splitting !== null

  function resetInput() {
    if (inputRef.current) inputRef.current.value = ''
  }

  function clearUpload() {
    setUpload(null)
    setCuts(new Set())
    setZoomPage(null)
    setNotice('')
    resetInput()
  }

  const stage = useCallback(async (file: File | undefined) => {
    resetInput()
    if (!file) return
    if (!isPdf(file)) {
      setError('Choose a PDF. Other formats can be uploaded from the Files tab.')
      return
    }

    try {
      setReading(true)
      setError('')
      setNotice('')
      setResult(null)
      setCuts(new Set())
      setZoomPage(null)
      setUpload(await uploadSplitPdf(file))
    } catch (err) {
      setUpload(null)
      setError(errorMessage(err, 'Failed to read the PDF'))
    } finally {
      setReading(false)
    }
  }, [])

  function onDragOver(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
  }

  function onDragEnter(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    setDragging(true)
  }

  function onDragLeave(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    if (event.currentTarget.contains(event.relatedTarget as Node)) return
    setDragging(false)
  }

  function onDrop(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    setDragging(false)
    void stage(event.dataTransfer.files?.[0])
  }

  function toggleCut(page: number) {
    setCuts((current) => {
      const next = new Set(current)
      if (next.has(page)) next.delete(page)
      else next.add(page)
      return next
    })
  }

  async function onDetect() {
    if (!upload) return
    try {
      setError('')
      setNotice('')
      setDetecting({ done: 0, total: upload.page_count })
      const suggestion = await detectSplitParts(upload.upload_id, setDetecting)
      setCuts(cutsFromParts(suggestion.parts, upload.page_count))
      setNotice(
        `Proposed ${plural(suggestion.parts.length, 'document')}${
          suggestion.text_source === 'ocr' ? ', read by OCR' : ''
        }. Adjust the cuts if they are off.`,
      )
    } catch (err) {
      setError(errorMessage(err, 'Detection failed'))
    } finally {
      setDetecting(null)
    }
  }

  async function onSplit() {
    if (!upload) return
    try {
      setError('')
      setNotice('')
      setSplitting({ done: 0, total: parts.length })
      const splitResult = await runSplit(upload.upload_id, parts, setSplitting)
      // Splitting consumes the upload, so the marking panel must not linger.
      clearUpload()
      setResult(splitResult)
    } catch (err) {
      setError(errorMessage(err, 'Split failed'))
    } finally {
      setSplitting(null)
    }
  }

  async function onCancel() {
    if (!upload) return
    const uploadId = upload.upload_id
    clearUpload()
    setError('')
    try {
      await discardSplitUpload(uploadId)
    } catch {
      // Best-effort cleanup: staged uploads expire on their own.
    }
  }

  return (
    <section className="flex flex-col gap-5">
      <div>
        <h2 className="font-display text-xl font-semibold text-ink">Split documents</h2>
        <p className="mt-1 text-sm text-ink-soft">
          Upload a PDF that holds several separate documents joined into one file, splitting it into
          one document per part. Mark where each document starts, or let the AI propose the cuts.
          The original file is not kept.
        </p>
      </div>

      {!upload && !busy && (
        <label
          className={`flex min-h-44 cursor-pointer flex-col items-center justify-center gap-1 rounded-none border border-dashed p-6 text-center transition-colors ${
            dragging
              ? 'border-ink bg-bright'
              : 'border-line-strong bg-surface hover:border-ink/50 hover:bg-bright'
          }`}
          onDragOver={onDragOver}
          onDragEnter={onDragEnter}
          onDragLeave={onDragLeave}
          onDrop={onDrop}
        >
          <input
            ref={inputRef}
            type="file"
            accept={ACCEPT_ATTR}
            disabled={reading}
            onChange={(event) => void stage(event.target.files?.[0] ?? undefined)}
            className="hidden"
          />
          <span className="text-sm font-medium text-ink">
            {reading ? 'Reading PDF…' : 'Choose a PDF to split'}
          </span>
          {!reading && <span className="text-xs text-ink-faint">or drop it here</span>}
        </label>
      )}

      {upload && (
        <div className="flex flex-col gap-4 rounded-none border border-line bg-bright p-5">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <p className="text-sm font-medium text-ink">{upload.file_name || 'Scanned PDF'}</p>
              <p className="mt-1 text-sm text-ink-soft">
                {plural(upload.page_count, 'page')}. Click a gap between pages to cut there.
              </p>
            </div>
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => void onDetect()}>
              Detect automatically
            </Button>
          </div>

          <ol className="flex flex-wrap items-stretch gap-0">
            {Array.from({ length: upload.page_count }, (_, index) => index + 1).map((page) => (
              <li key={page} className="flex items-stretch">
                <button
                  type="button"
                  onClick={() => setZoomPage(page)}
                  aria-label={`Enlarge page ${page}`}
                  className="flex w-24 flex-col items-center gap-1 border border-line bg-surface p-1 transition-colors hover:border-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood"
                >
                  {thumbs[page] ? (
                    <img
                      src={thumbs[page]}
                      alt={`Page ${page}`}
                      className="h-28 w-full object-contain object-top"
                    />
                  ) : (
                    <span className="flex h-28 w-full items-center justify-center text-xs text-ink-faint">
                      …
                    </span>
                  )}
                  <span className="text-[11px] tracking-[0.08em] text-ink-soft">{page}</span>
                </button>
                {page < upload.page_count && (
                  <button
                    type="button"
                    onClick={() => toggleCut(page)}
                    disabled={busy}
                    aria-pressed={cuts.has(page)}
                    aria-label={`Split after page ${page}`}
                    title={cuts.has(page) ? `Remove the cut after page ${page}` : `Split after page ${page}`}
                    className={`w-7 shrink-0 text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood ${
                      cuts.has(page)
                        ? 'bg-oxblood text-paper'
                        : 'text-ink-faint hover:bg-surface hover:text-ink'
                    }`}
                  >
                    ✂
                  </button>
                )}
              </li>
            ))}
          </ol>

          {zoomPage !== null && thumbs[zoomPage] && (
            <div className="flex flex-col items-start gap-2 border border-line-strong bg-surface p-3">
              <div className="flex w-full items-center justify-between gap-3">
                <p className="text-xs font-semibold uppercase tracking-[0.12em] text-ink-soft">
                  Page {zoomPage}
                </p>
                <button
                  type="button"
                  onClick={() => setZoomPage(null)}
                  className="text-xs font-medium text-ink-soft hover:text-ink"
                >
                  Close
                </button>
              </div>
              <img
                src={thumbs[zoomPage]}
                alt={`Page ${zoomPage}, enlarged`}
                className="max-h-[36rem] w-auto max-w-full"
              />
            </div>
          )}

          <p className="text-sm font-medium text-ink">
            {plural(parts.length, 'document')}: pages {describeParts(parts)}
          </p>

          {notice && <p className="text-sm text-ink-soft">{notice}</p>}

          <div className="flex flex-wrap gap-2">
            <Button disabled={busy} onClick={() => void onSplit()}>
              Split into {plural(parts.length, 'document')}
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => void onCancel()}>
              Cancel
            </Button>
          </div>
        </div>
      )}

      {detecting && (
        <div className="rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            Analysing page {detecting.done} of {detecting.total}…
          </p>
          <p className="mt-1 text-sm text-ink-soft">
            Reading each page to find where the documents start.
          </p>
        </div>
      )}

      {splitting && (
        <div className="rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            Creating {splitting.done} of {splitting.total}…
          </p>
          <p className="mt-1 text-sm text-ink-soft">
            New documents are queued for OCR and AI processing.
          </p>
        </div>
      )}

      {result && (
        <div className="flex flex-col gap-3 rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            Created {plural(result.created, 'document')}.
          </p>
          <ul className="text-sm text-ink-soft">
            {result.skipped_duplicates > 0 && (
              <li>{plural(result.skipped_duplicates, 'part')} already in your library.</li>
            )}
            {result.skipped_oversized > 0 && (
              <li>{plural(result.skipped_oversized, 'part')} skipped as too large.</li>
            )}
            {result.failed > 0 && <li>{plural(result.failed, 'part')} failed.</li>}
          </ul>
          {result.errors.length > 0 && (
            <ul className="flex flex-col gap-1 text-sm text-madder">
              {result.errors.map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          )}
          <Link to="/" className="text-sm font-medium text-oxblood underline">
            Open documents
          </Link>
        </div>
      )}

      {error && <p className="text-sm text-madder">{error}</p>}
    </section>
  )
}
