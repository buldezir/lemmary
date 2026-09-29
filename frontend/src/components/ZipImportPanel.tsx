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
import { t, tNode, type MessageKey } from '../i18n'
import { Button } from './ui'

const ACCEPT_ATTR = '.zip,application/zip,application/x-zip-compressed'

// Both sources run the identical stage -> preview -> confirm -> poll flow
// against the identical endpoints; only the copy differs.
const COPY: Record<
  ZipImportSource,
  {
    heading: string
    description: ReactNode
    chooseLabel: string
    fileNameFallback: string
    /** The "Found 12 files" plural. */
    found: MessageKey
    allDuplicates: string
  }
> = {
  amazon: {
    heading: t('zipImportPanel.amazonHeading'),
    description: t('zipImportPanel.amazonDescription'),
    chooseLabel: t('zipImportPanel.amazonChoose'),
    fileNameFallback: t('zipImportPanel.amazonFallback'),
    found: 'zipImportPanel.amazonFound',
    allDuplicates: t('zipImportPanel.amazonAllDuplicates'),
  },
  zip: {
    heading: t('zipImportPanel.zipHeading'),
    description: tNode('zipImportPanel.zipDescription', {
      link: (
        <Link to="/import/archive" className="font-medium text-oxblood underline">
          {t('zipImportPanel.zipLink')}
        </Link>
      ),
    }),
    chooseLabel: t('zipImportPanel.zipChoose'),
    fileNameFallback: t('zipImportPanel.zipFallback'),
    found: 'zipImportPanel.zipFound',
    allDuplicates: t('zipImportPanel.zipAllDuplicates'),
  },
}

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
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
      setError(err instanceof Error ? err.message : t('zipImportPanel.readFailed'))
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
      setError(err instanceof Error ? err.message : t('zipImportPanel.importFailed'))
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
            {reading ? t('zipImportPanel.reading') : copy.chooseLabel}
          </span>
          {!reading && (
            <span className="text-xs text-ink-faint">{t('zipImportPanel.nothingYet')}</span>
          )}
        </label>
      )}

      {preview && !importing && (
        <div className="flex flex-col gap-4 rounded-none border border-line bg-bright p-5">
          <div>
            <p className="text-sm font-medium text-ink">
              {preview.file_name || copy.fileNameFallback}
            </p>
            <p className="mt-1 text-sm text-ink-soft">
              {t(copy.found, { count: preview.file_count })}:{' '}
              {[
                t('zipImportPanel.new', { count: preview.importable_count }),
                preview.duplicate_count > 0 &&
                  t('zipImportPanel.duplicates', { count: preview.duplicate_count }),
                preview.oversized_count > 0 &&
                  t('zipImportPanel.oversized', { count: preview.oversized_count }),
              ]
                .filter(Boolean)
                .join(', ')}
              .
              {preview.ignored_count > 0 &&
                ` ${t('zipImportPanel.ignored', { count: preview.ignored_count })}`}
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
                    ? t('zipImportPanel.tooLarge')
                    : file.duplicate
                      ? t('zipImportPanel.duplicate')
                      : formatBytes(file.size)}
                </span>
              </li>
            ))}
          </ul>

          <p className="text-sm font-medium text-ink">
            {t('zipImportPanel.confirm', { count: preview.file_count })}
          </p>

          {preview.importable_count === 0 && (
            <p className="text-sm text-ink-soft">
              {preview.duplicate_count === preview.file_count
                ? copy.allDuplicates
                : t('zipImportPanel.nothingImportable')}
            </p>
          )}

          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void onConfirm()} disabled={preview.importable_count === 0}>
              {t('zipImportPanel.import', { count: preview.importable_count })}
            </Button>
            <Button variant="secondary" onClick={() => void onCancel()}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}

      {importing && (
        <div className="rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            {t('zipImportPanel.importing', { done: progress.done, total: progress.total })}
          </p>
          <p className="mt-1 text-sm text-ink-soft">
            {t('zipImportPanel.queued')}
          </p>
        </div>
      )}

      {result && (
        <div className="flex flex-col gap-3 rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            {t('zipImportPanel.imported', { count: result.imported })}
          </p>
          <ul className="text-sm text-ink-soft">
            {result.skipped_duplicates > 0 && (
              <li>{t('zipImportPanel.skippedDuplicates', { count: result.skipped_duplicates })}</li>
            )}
            {result.skipped_oversized > 0 && (
              <li>{t('zipImportPanel.skippedOversized', { count: result.skipped_oversized })}</li>
            )}
            {result.failed > 0 && <li>{t('zipImportPanel.failed', { count: result.failed })}</li>}
          </ul>
          {result.errors.length > 0 && (
            <ul className="flex flex-col gap-1 text-sm text-madder">
              {result.errors.map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          )}
          <Link to={documentsLanding()} className="text-sm font-medium text-oxblood underline">
            {documentsLanding() === '/inbox'
              ? t('zipImportPanel.openInbox')
              : t('zipImportPanel.openDocuments')}
          </Link>
        </div>
      )}

      {error && <p className="text-sm text-madder">{error}</p>}
    </section>
  )
}
