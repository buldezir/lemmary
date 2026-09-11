import { type ChangeEvent, type ReactNode, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  discardZipArchive,
  importZipArchive,
  uploadZipArchive,
  type ZipArchivePreview,
  type ZipImportProgress,
  type ZipImportResult,
  type ZipImportSource,
} from '../lib/api/imports'
import { documentsLanding } from '../lib/reviewPolicy'
import { Button } from './ui'

const ACCEPT_ATTR = '.zip,application/zip,application/x-zip-compressed'

// The two sources run the identical stage -> preview -> confirm -> poll flow
// against the identical endpoints; all that differs is what the user is looking
// at while they do it. Keeping the copy here rather than in props means the
// routes are one line each and the shapes cannot drift apart.
const COPY: Record<
  ZipImportSource,
  {
    heading: string
    description: ReactNode
    chooseLabel: string
    fileNameFallback: string
    /** Singular; the count noun in "Found 12 files". */
    countNoun: string
    allDuplicates: string
  }
> = {
  amazon: {
    heading: 'Import Amazon orders',
    description: (
      <>
        Import an archive of your Amazon order history. Request it from Amazon under Account
        &rarr; Request your data &rarr; Your Orders; Amazon emails a download link once the export
        is ready. Only the invoice PDFs are imported — the CSV reports and delivery photos in the
        archive are ignored.
      </>
    ),
    chooseLabel: 'Choose the order export (.zip)',
    fileNameFallback: 'Order export',
    countNoun: 'PDF file',
    allDuplicates: 'Every PDF in this archive is already in your library.',
  },
  zip: {
    heading: 'Import a zip archive',
    description: (
      <>
        Import a zip of documents you packed yourself — PDF, JPEG, PNG, WebP, plain text, CSV,
        Word (.docx) or Excel (.xlsx). Folders inside the archive are kept as a name prefix, and
        anything else in it is ignored. To restore a Lemmary backup instead, use{' '}
        <Link to="/import/archive" className="font-medium text-oxblood underline">
          Import archive
        </Link>
        .
      </>
    ),
    chooseLabel: 'Choose a zip archive',
    fileNameFallback: 'Archive',
    countNoun: 'file',
    allDuplicates: 'Every file in this archive is already in your library.',
  },
}

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

function plural(count: number, word: string) {
  return `${count} ${word}${count === 1 ? '' : 's'}`
}

export function ZipImportPanel({ source }: { source: ZipImportSource }) {
  const copy = COPY[source]
  const inputRef = useRef<HTMLInputElement>(null)
  const [reading, setReading] = useState(false)
  const [preview, setPreview] = useState<ZipArchivePreview | null>(null)
  const [progress, setProgress] = useState<ZipImportProgress | null>(null)
  const [result, setResult] = useState<ZipImportResult | null>(null)
  const [error, setError] = useState('')

  const importing = progress !== null

  function resetInput() {
    if (inputRef.current) inputRef.current.value = ''
  }

  async function onArchiveSelected(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    resetInput()
    if (!file) return

    try {
      setReading(true)
      setError('')
      setResult(null)
      setPreview(await uploadZipArchive(source, file))
    } catch (err) {
      setPreview(null)
      setError(err instanceof Error ? err.message : 'Failed to read the archive')
    } finally {
      setReading(false)
    }
  }

  async function onConfirm() {
    if (!preview) return
    const uploadId = preview.upload_id
    try {
      setError('')
      setProgress({ done: 0, total: preview.file_count })
      // Starting the import consumes the staged archive, so the confirmation
      // panel must not linger with a button that can no longer be used.
      setPreview(null)
      setResult(await importZipArchive(source, uploadId, setProgress))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Import failed')
    } finally {
      setProgress(null)
    }
  }

  async function onCancel() {
    if (!preview) return
    const uploadId = preview.upload_id
    setPreview(null)
    setError('')
    try {
      await discardZipArchive(source, uploadId)
    } catch {
      // Best-effort cleanup: staged archives expire on their own.
    }
  }

  return (
    <section className="flex flex-col gap-5">
      <div>
        <h2 className="font-display text-xl font-semibold text-ink">{copy.heading}</h2>
        <p className="mt-1 text-sm text-ink-soft">{copy.description}</p>
      </div>

      {!preview && !importing && (
        <label
          className={`flex min-h-44 cursor-pointer flex-col items-center justify-center gap-1 rounded-none border border-dashed p-6 text-center transition-colors ${
            reading
              ? 'border-line-strong bg-surface'
              : 'border-line-strong bg-surface hover:border-ink/50 hover:bg-bright'
          }`}
        >
          <input
            ref={inputRef}
            type="file"
            accept={ACCEPT_ATTR}
            disabled={reading}
            onChange={onArchiveSelected}
            className="hidden"
          />
          <span className="text-sm font-medium text-ink">
            {reading ? 'Reading archive…' : copy.chooseLabel}
          </span>
          {!reading && <span className="text-xs text-ink-faint">Nothing is imported yet</span>}
        </label>
      )}

      {preview && !importing && (
        <div className="flex flex-col gap-4 rounded-none border border-line bg-bright p-5">
          <div>
            <p className="text-sm font-medium text-ink">
              {preview.file_name || copy.fileNameFallback}
            </p>
            <p className="mt-1 text-sm text-ink-soft">
              Found {plural(preview.file_count, copy.countNoun)}: {preview.importable_count} new
              {preview.duplicate_count > 0 &&
                `, ${preview.duplicate_count} already in your library`}
              {preview.oversized_count > 0 && `, ${preview.oversized_count} too large`}.
              {preview.ignored_count > 0 &&
                ` ${plural(preview.ignored_count, 'other file')} in the archive ignored.`}
            </p>
          </div>

          <ul className="max-h-64 divide-y divide-line/50 overflow-y-auto rounded-xs border border-line">
            {preview.files.map((file) => (
              <li key={file.path} className="flex items-start justify-between gap-3 px-3 py-2">
                <div className="min-w-0">
                  <p className="truncate text-sm text-ink">{file.name}</p>
                  <p className="truncate text-xs text-ink-faint">{file.path}</p>
                </div>
                <span className="shrink-0 text-xs text-ink-faint">
                  {file.oversized
                    ? 'Too large'
                    : file.duplicate
                      ? 'Duplicate'
                      : formatBytes(file.size)}
                </span>
              </li>
            ))}
          </ul>

          <p className="text-sm font-medium text-ink">
            Do you actually want to import {plural(preview.file_count, 'file')} (duplicates will be
            ignored)?
          </p>

          {preview.importable_count === 0 && (
            <p className="text-sm text-ink-soft">{copy.allDuplicates}</p>
          )}

          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void onConfirm()} disabled={preview.importable_count === 0}>
              Import {plural(preview.importable_count, 'file')}
            </Button>
            <Button variant="secondary" onClick={() => void onCancel()}>
              Cancel
            </Button>
          </div>
        </div>
      )}

      {importing && (
        <div className="rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            Importing {progress.done} of {progress.total}…
          </p>
          <p className="mt-1 text-sm text-ink-soft">
            Imported documents are queued for OCR and AI processing.
          </p>
        </div>
      )}

      {result && (
        <div className="flex flex-col gap-3 rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            Imported {plural(result.imported, 'document')}.
          </p>
          <ul className="text-sm text-ink-soft">
            {result.skipped_duplicates > 0 && (
              <li>{plural(result.skipped_duplicates, 'duplicate')} ignored.</li>
            )}
            {result.skipped_oversized > 0 && (
              <li>{plural(result.skipped_oversized, 'file')} skipped as too large.</li>
            )}
            {result.failed > 0 && <li>{plural(result.failed, 'file')} failed.</li>}
          </ul>
          {result.errors.length > 0 && (
            <ul className="flex flex-col gap-1 text-sm text-madder">
              {result.errors.map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          )}
          <Link to={documentsLanding()} className="text-sm font-medium text-oxblood underline">
            {documentsLanding() === '/inbox' ? 'Open the Inbox' : 'Open documents'}
          </Link>
        </div>
      )}

      {error && <p className="text-sm text-madder">{error}</p>}
    </section>
  )
}
