import { type ChangeEvent, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  discardArchive,
  importArchive,
  uploadArchive,
  type ArchiveImportMode,
  type ArchiveImportProgress,
  type ArchiveImportResult,
  type ArchivePreview,
} from '../lib/api/imports'
import { Button, labelTextClassName } from '../components/ui'

const ACCEPT_ATTR = '.zip,application/zip,application/x-zip-compressed'

const modeOptions: { value: ArchiveImportMode; label: string; description: string }[] = [
  {
    value: 'restore',
    label: 'Restore the archive as it was',
    description:
      'Bring back titles, tags, correspondents, document types, dates, OCR text and thumbnails exactly as the archive holds them. Restored documents are not processed at all, so nothing is sent to OCR or the AI provider.',
  },
  {
    value: 'reprocess',
    label: 'Import the files only and reprocess',
    description:
      'Ignore the metadata in the archive and run the full OCR and AI pipeline, as if each file had just been uploaded.',
  },
]

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

function plural(count: number, word: string) {
  return `${count} ${word}${count === 1 ? '' : 's'}`
}

/** Pluralising a three-noun phrase needs more than a trailing "s". */
function taxonomyLabel(count: number) {
  return count === 1
    ? '1 tag, correspondent or document type'
    : `${count} tags, correspondents and document types`
}

/** Entries that will become documents, i.e. everything not being skipped. */
function importableFiles(preview: ArchivePreview) {
  return preview.files.filter((file) => !file.duplicate && !file.oversized && !file.missing)
}

function entryStatus(file: ArchivePreview['files'][number]) {
  if (file.missing) return 'Missing'
  if (file.oversized) return 'Too large'
  if (file.duplicate) return 'Duplicate'
  return formatBytes(file.size)
}

export function ImportArchivePage() {
  const inputRef = useRef<HTMLInputElement>(null)
  const [reading, setReading] = useState(false)
  const [preview, setPreview] = useState<ArchivePreview | null>(null)
  const [mode, setMode] = useState<ArchiveImportMode>('restore')
  const [progress, setProgress] = useState<ArchiveImportProgress | null>(null)
  const [result, setResult] = useState<ArchiveImportResult | null>(null)
  const [error, setError] = useState('')

  const importing = progress !== null

  // A restore can be worth running with no new documents at all: the archive's
  // tags, correspondents and document types are restored either way, and an
  // archive can carry taxonomy that no document references.
  const taxonomyOnly =
    preview !== null &&
    mode === 'restore' &&
    preview.importable_count === 0 &&
    preview.taxonomy_count > 0
  const canImport = preview !== null && (preview.importable_count > 0 || taxonomyOnly)
  // Without a metadata sidecar there is nothing to restore, so those go through
  // the normal pipeline instead -- which only older archives contain.
  const withoutMetadata =
    preview === null ? 0 : importableFiles(preview).filter((file) => !file.has_metadata).length

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
      setPreview(await uploadArchive(file))
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
      setProgress({ done: 0, total: preview.document_count })
      // Starting the import consumes the staged archive, so the confirmation
      // panel must not linger with a button that can no longer be used.
      setPreview(null)
      setResult(await importArchive(uploadId, mode, setProgress))
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
      await discardArchive(uploadId)
    } catch {
      // Best-effort cleanup: staged archives expire on their own.
    }
  }

  return (
    <section className="flex flex-col gap-5">
      <div>
        <h2 className="font-display text-xl font-semibold text-ink">Restore a Lemmary archive</h2>
        <p className="mt-1 text-sm text-ink-soft">
          Restore a backup downloaded from{' '}
          <Link to="/export" className="font-medium text-oxblood underline">
            Export
          </Link>
          , on this instance or another one. Documents already in your library are skipped, so
          restoring the same archive twice is safe.
        </p>
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
            {reading ? 'Reading archive…' : 'Choose a Lemmary archive (.zip)'}
          </span>
          {!reading && <span className="text-xs text-ink-faint">Nothing is imported yet</span>}
        </label>
      )}

      {preview && !importing && (
        <div className="flex flex-col gap-4 rounded-none border border-line bg-bright p-5">
          <div>
            <p className="text-sm font-medium text-ink">{preview.file_name || 'Lemmary archive'}</p>
            <p className="mt-1 text-sm text-ink-soft">
              Found {plural(preview.document_count, 'document')}: {preview.importable_count} new
              {preview.duplicate_count > 0 &&
                `, ${preview.duplicate_count} already in your library`}
              {preview.oversized_count > 0 && `, ${preview.oversized_count} too large`}
              {preview.missing_count > 0 && `, ${preview.missing_count} missing from the archive`}.
              {preview.taxonomy_count > 0 &&
                ` ${taxonomyLabel(preview.taxonomy_count)} will be restored alongside them.`}
              {preview.ignored_count > 0 &&
                ` ${plural(preview.ignored_count, 'other entry')} ignored.`}
            </p>
            {mode === 'restore' && withoutMetadata > 0 && (
              <p className="mt-2 text-sm text-ink-soft">
                The archive carries no metadata for {plural(withoutMetadata, 'document')}, so
                {withoutMetadata === 1 ? ' it is' : ' they are'} processed like a new upload
                instead of restored.
              </p>
            )}
            {!preview.has_manifest && (
              <p className="mt-2 text-sm text-ink-soft">
                This archive predates the backup manifest, so it is read from its file names alone.
                Tags that no document uses cannot be recovered from it.
              </p>
            )}
          </div>

          <ul className="max-h-64 divide-y divide-line/50 overflow-y-auto rounded-xs border border-line">
            {preview.files.map((file) => (
              <li
                key={file.document_id || file.path}
                className="flex items-start justify-between gap-3 px-3 py-2"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm text-ink">{file.title || file.name}</p>
                  <p className="truncate text-xs text-ink-faint">{file.name}</p>
                </div>
                <span className="shrink-0 text-xs text-ink-faint">{entryStatus(file)}</span>
              </li>
            ))}
          </ul>

          <fieldset className="space-y-2">
            <legend className={labelTextClassName}>Import mode</legend>
            {modeOptions.map((option) => (
              <label
                key={option.value}
                className={`flex cursor-pointer items-start gap-2 rounded-xs border px-3 py-2 text-sm ${
                  mode === option.value
                    ? 'border-ink bg-bright text-ink'
                    : 'border-line bg-surface text-ink-muted'
                }`}
              >
                <input
                  type="radio"
                  className="mt-0.5"
                  name="archive-import-mode"
                  value={option.value}
                  checked={mode === option.value}
                  onChange={() => setMode(option.value)}
                />
                <span>
                  <span className="font-medium">{option.label}</span>
                  <span className="mt-0.5 block text-xs font-normal text-ink-soft">
                    {option.description}
                  </span>
                </span>
              </label>
            ))}
          </fieldset>

          {preview.importable_count === 0 && (
            <p className="text-sm text-ink-soft">
              Every document in this archive is already in your library.
              {taxonomyOnly &&
                ' Restoring will still bring back its tags, correspondents and document types.'}
            </p>
          )}

          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void onConfirm()} disabled={!canImport}>
              {taxonomyOnly
                ? `Restore ${taxonomyLabel(preview.taxonomy_count)}`
                : `Import ${plural(preview.importable_count, 'document')}`}
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
            {mode === 'restore'
              ? 'Restored documents go straight into your library; nothing is sent to OCR or the AI provider.'
              : 'Imported documents are queued for OCR and AI processing.'}
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
            {result.tags_upserted > 0 && <li>{plural(result.tags_upserted, 'tag')} created.</li>}
            {result.correspondents_upserted > 0 && (
              <li>{plural(result.correspondents_upserted, 'correspondent')} created.</li>
            )}
            {result.document_types_upserted > 0 && (
              <li>{plural(result.document_types_upserted, 'document type')} created.</li>
            )}
            {result.failed > 0 && <li>{plural(result.failed, 'document')} failed.</li>}
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
