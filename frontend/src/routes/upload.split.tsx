import { type DragEvent, type KeyboardEvent, useCallback, useEffect, useRef, useState } from 'react'
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
import { documentsLanding } from '../lib/reviewPolicy'
import { Button } from '../components/ui'
import { useStoredValue } from '../hooks/useStoredFlag'
import { t } from '../i18n'

const ACCEPT_ATTR = '.pdf,application/pdf'

/** How many thumbnails are fetched at once, so a long scan still paints early. */
const THUMB_CONCURRENCY = 4

const THUMB_SIZES = [
  { label: 'uploadSplit.sizeSmall', tile: 'w-24', image: 'h-28' },
  { label: 'uploadSplit.sizeMedium', tile: 'w-40', image: 'h-52' },
  { label: 'uploadSplit.sizeLarge', tile: 'w-64', image: 'h-80' },
] as const

function isPdf(file: File) {
  return file.name.toLowerCase().endsWith('.pdf') || file.type === 'application/pdf'
}

function errorMessage(err: unknown, fallback: string) {
  return err instanceof Error && err.message ? err.message : fallback
}

/** Revoked on unmount, so a long scan does not leak blobs. */
function usePageThumbs(uploadId: string | undefined, pageCount: number) {
  // Keyed by upload so a stale map is ignored when the upload changes, rather
  // than needing a synchronous reset inside the effect.
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

type PageViewerProps = {
  page: number
  pageCount: number
  src: string | undefined
  cut: boolean
  busy: boolean
  onPage: (page: number) => void
  onToggleCut: (page: number) => void
  onClose: () => void
}

/** Full-size page in a modal, so cuts can be set while reading. Mounted only while open. */
function PageViewer({ page, pageCount, src, cut, busy, onPage, onToggleCut, onClose }: PageViewerProps) {
  const ref = useRef<HTMLDialogElement>(null)
  const hasPrevious = page > 1
  const hasNext = page < pageCount

  useEffect(() => {
    ref.current?.showModal()
  }, [])

  function onKeyDown(event: KeyboardEvent<HTMLDialogElement>) {
    if (event.key === 'ArrowLeft' && hasPrevious) onPage(page - 1)
    else if (event.key === 'ArrowRight' && hasNext) onPage(page + 1)
  }

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onKeyDown={onKeyDown}
      onClick={(event) => event.target === ref.current && ref.current.close()}
      aria-label={t('uploadSplit.page', { page })}
      className="m-auto max-h-[96vh] max-w-[96vw] border border-line bg-surface p-0 text-ink backdrop:bg-ink/60"
    >
      <div className="flex flex-col gap-3 p-3">
        <div className="flex items-center justify-between gap-3">
          <p className="text-xs font-semibold uppercase tracking-[0.12em] text-ink-soft">
            {t('uploadSplit.page', { page })} / {pageCount}
          </p>
          <Button variant="secondary" size="sm" onClick={() => ref.current?.close()}>
            {t('common.close')}
          </Button>
        </div>
        {src ? (
          <img
            src={src}
            alt={t('uploadSplit.enlarged', { page })}
            className="mx-auto max-h-[calc(96vh-8rem)] w-auto max-w-full object-contain"
          />
        ) : (
          <span className="flex h-[60vh] w-[45vh] items-center justify-center text-sm text-ink-faint">…</span>
        )}
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Button variant="secondary" size="sm" disabled={!hasPrevious} onClick={() => onPage(page - 1)}>
            ← {t('uploadSplit.previousPage')}
          </Button>
          {hasNext && (
            <Button
              variant={cut ? 'primary' : 'secondary'}
              size="sm"
              disabled={busy}
              aria-pressed={cut}
              onClick={() => onToggleCut(page)}
            >
              ✂ {t('uploadSplit.cutHere')}
            </Button>
          )}
          <Button variant="secondary" size="sm" disabled={!hasNext} onClick={() => onPage(page + 1)}>
            {t('uploadSplit.nextPage')} →
          </Button>
        </div>
      </div>
    </dialog>
  )
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
  const [thumbSize, setThumbSize] = useStoredValue<number>('lemmary.splitThumbSize', 0, (stored) =>
    THUMB_SIZES[Number(stored)] ? Number(stored) : undefined,
  )
  const size = THUMB_SIZES[thumbSize] ?? THUMB_SIZES[0]

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
      setError(t('uploadSplit.notPdf'))
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
      setError(errorMessage(err, t('uploadSplit.readFailed')))
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
        t(suggestion.text_source === 'ocr' ? 'uploadSplit.proposedOcr' : 'uploadSplit.proposed', {
          count: suggestion.parts.length,
        }),
      )
    } catch (err) {
      setError(errorMessage(err, t('uploadSplit.detectFailed')))
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
      setError(errorMessage(err, t('uploadSplit.splitFailed')))
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
        <h2 className="font-display text-xl font-semibold text-ink">{t('uploadSplit.title')}</h2>
        <p className="mt-1 text-sm text-ink-soft">
          {t('uploadSplit.intro')}
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
            {reading ? t('uploadSplit.reading') : t('uploadSplit.choose')}
          </span>
          {!reading && <span className="text-xs text-ink-faint">{t('uploadSplit.dropHint')}</span>}
        </label>
      )}

      {upload && (
        <div className="flex flex-col gap-4 rounded-none border border-line bg-bright p-5">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <p className="text-sm font-medium text-ink">{upload.file_name || t('uploadSplit.defaultName')}</p>
              <p className="mt-1 text-sm text-ink-soft">
                {t('uploadSplit.pages', { count: upload.page_count })}
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <div role="group" aria-label={t('uploadSplit.thumbSize')} className="flex border border-line">
                {THUMB_SIZES.map((option, index) => (
                  <button
                    key={option.label}
                    type="button"
                    aria-pressed={thumbSize === index}
                    onClick={() => setThumbSize(index)}
                    className={`px-2 py-1 text-xs transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood ${
                      thumbSize === index ? 'bg-ink text-paper' : 'text-ink-soft hover:bg-surface hover:text-ink'
                    }`}
                  >
                    {t(option.label)}
                  </button>
                ))}
              </div>
              <Button variant="secondary" size="sm" disabled={busy} onClick={() => void onDetect()}>
                {t('uploadSplit.detect')}
              </Button>
            </div>
          </div>

          <ol className="flex flex-wrap items-stretch gap-0">
            {Array.from({ length: upload.page_count }, (_, index) => index + 1).map((page) => (
              <li key={page} className="flex items-stretch">
                <button
                  type="button"
                  onClick={() => setZoomPage(page)}
                  aria-label={t('uploadSplit.enlarge', { page })}
                  className={`flex ${size.tile} cursor-zoom-in flex-col items-center gap-1 border border-line bg-surface p-1 transition-colors hover:border-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood`}
                >
                  {thumbs[page] ? (
                    <img
                      src={thumbs[page]}
                      alt={t('uploadSplit.page', { page })}
                      className={`${size.image} w-full object-contain object-top`}
                    />
                  ) : (
                    <span className={`flex ${size.image} w-full items-center justify-center text-xs text-ink-faint`}>
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
                    aria-label={t('uploadSplit.splitAfter', { page })}
                    title={
                      cuts.has(page)
                        ? t('uploadSplit.removeCut', { page })
                        : t('uploadSplit.splitAfter', { page })
                    }
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

          {zoomPage !== null && (
            <PageViewer
              page={zoomPage}
              pageCount={upload.page_count}
              src={thumbs[zoomPage]}
              cut={cuts.has(zoomPage)}
              busy={busy}
              onPage={setZoomPage}
              onToggleCut={toggleCut}
              onClose={() => setZoomPage(null)}
            />
          )}

          <p className="text-sm font-medium text-ink">
            {t('uploadSplit.summary', { count: parts.length, ranges: describeParts(parts) })}
          </p>

          {notice && <p className="text-sm text-ink-soft">{notice}</p>}

          <div className="flex flex-wrap gap-2">
            <Button disabled={busy} onClick={() => void onSplit()}>
              {t('uploadSplit.splitInto', { count: parts.length })}
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => void onCancel()}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}

      {detecting && (
        <div className="rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            {t('uploadSplit.analysing', { done: detecting.done, total: detecting.total })}
          </p>
          <p className="mt-1 text-sm text-ink-soft">
            {t('uploadSplit.analysingHint')}
          </p>
        </div>
      )}

      {splitting && (
        <div className="rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            {t('uploadSplit.creating', { done: splitting.done, total: splitting.total })}
          </p>
          <p className="mt-1 text-sm text-ink-soft">
            {t('uploadSplit.creatingHint')}
          </p>
        </div>
      )}

      {result && (
        <div className="flex flex-col gap-3 rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            {t('uploadSplit.created', { count: result.created })}
          </p>
          <ul className="text-sm text-ink-soft">
            {result.skipped_duplicates > 0 && (
              <li>{t('uploadSplit.partsDuplicate', { count: result.skipped_duplicates })}</li>
            )}
            {result.skipped_oversized > 0 && (
              <li>{t('uploadSplit.partsOversized', { count: result.skipped_oversized })}</li>
            )}
            {result.failed > 0 && <li>{t('uploadSplit.partsFailed', { count: result.failed })}</li>}
          </ul>
          {result.errors.length > 0 && (
            <ul className="flex flex-col gap-1 text-sm text-madder">
              {result.errors.map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          )}
          <Link to={documentsLanding()} className="text-sm font-medium text-oxblood underline">
            {documentsLanding() === '/inbox' ? t('uploadScan.openInbox') : t('uploadScan.openDocuments')}
          </Link>
        </div>
      )}

      {error && <p className="text-sm text-madder">{error}</p>}
    </section>
  )
}
