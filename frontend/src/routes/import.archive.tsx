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
import { t, tNode } from '../i18n'

const ACCEPT_ATTR = '.zip,application/zip,application/x-zip-compressed'

const modeOptions: { value: ArchiveImportMode; label: string; description: string }[] = [
  {
    value: 'restore',
    label: t('importArchive.restoreLabel'),
    description: t('importArchive.restoreDescription'),
  },
  {
    value: 'reprocess',
    label: t('importArchive.reprocessLabel'),
    description: t('importArchive.reprocessDescription'),
  },
]

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

function importableFiles(preview: ArchivePreview) {
  return preview.files.filter((file) => !file.duplicate && !file.oversized && !file.missing)
}

function entryStatus(file: ArchivePreview['files'][number]) {
  if (file.missing) return t('importArchive.statusMissing')
  if (file.oversized) return t('importArchive.statusTooLarge')
  if (file.duplicate) return t('importArchive.statusDuplicate')
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

  // A restore can be worth running with no new documents at all: an archive can
  // carry taxonomy that no document references.
  const taxonomyOnly =
    preview !== null &&
    mode === 'restore' &&
    preview.importable_count === 0 &&
    preview.taxonomy_count > 0
  const canImport = preview !== null && (preview.importable_count > 0 || taxonomyOnly)
  // Without a metadata sidecar there is nothing to restore, so those go through
  // the normal pipeline instead.
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
      setError(err instanceof Error ? err.message : t('importArchive.readFailed'))
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
      setError(err instanceof Error ? err.message : t('importArchive.failed'))
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
        <h2 className="font-display text-xl font-semibold text-ink">{t('importArchive.title')}</h2>
        <p className="mt-1 text-sm text-ink-soft">
          {tNode('importArchive.intro', {
            link: (
              <Link to="/export" className="font-medium text-oxblood underline">
                {t('importArchive.exportLink')}
              </Link>
            ),
          })}
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
            {reading ? t('importArchive.reading') : t('importArchive.choose')}
          </span>
          {!reading && <span className="text-xs text-ink-faint">{t('importArchive.nothingYet')}</span>}
        </label>
      )}

      {preview && !importing && (
        <div className="flex flex-col gap-4 rounded-none border border-line bg-bright p-5">
          <div>
            <p className="text-sm font-medium text-ink">{preview.file_name || t('importArchive.defaultName')}</p>
            <p className="mt-1 text-sm text-ink-soft">
              {t('importArchive.found', {
                count: preview.document_count,
                importable: preview.importable_count,
              })}
              {preview.duplicate_count > 0 &&
                t('importArchive.foundDuplicates', { count: preview.duplicate_count })}
              {preview.oversized_count > 0 &&
                t('importArchive.foundOversized', { count: preview.oversized_count })}
              {preview.missing_count > 0 &&
                t('importArchive.foundMissing', { count: preview.missing_count })}
              .
              {preview.taxonomy_count > 0 &&
                ` ${t('importArchive.taxonomyRestored', { count: preview.taxonomy_count })}`}
              {preview.ignored_count > 0 &&
                ` ${t('importArchive.ignored', { count: preview.ignored_count })}`}
            </p>
            {mode === 'restore' && withoutMetadata > 0 && (
              <p className="mt-2 text-sm text-ink-soft">
                {t('importArchive.withoutMetadata', { count: withoutMetadata })}
              </p>
            )}
            {!preview.has_manifest && (
              <p className="mt-2 text-sm text-ink-soft">
                {t('importArchive.noManifest')}
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
            <legend className={labelTextClassName}>{t('importArchive.mode')}</legend>
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
              {t('importArchive.allPresent')}
              {taxonomyOnly && ` ${t('importArchive.taxonomyStill')}`}
            </p>
          )}

          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void onConfirm()} disabled={!canImport}>
              {taxonomyOnly
                ? t('importArchive.restoreTaxonomy', { count: preview.taxonomy_count })
                : t('importArchive.importDocuments', { count: preview.importable_count })}
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
            {t('importArchive.progress', { done: progress.done, total: progress.total })}
          </p>
          <p className="mt-1 text-sm text-ink-soft">
            {mode === 'restore'
              ? t('importArchive.restoringNote')
              : t('importArchive.importingNote')}
          </p>
        </div>
      )}

      {result && (
        <div className="flex flex-col gap-3 rounded-none border border-line bg-bright p-5">
          <p className="text-sm font-medium text-ink">
            {t('importArchive.imported', { count: result.imported })}
          </p>
          <ul className="text-sm text-ink-soft">
            {result.skipped_duplicates > 0 && (
              <li>{t('importArchive.duplicatesIgnored', { count: result.skipped_duplicates })}</li>
            )}
            {result.skipped_oversized > 0 && (
              <li>{t('importArchive.oversizedSkipped', { count: result.skipped_oversized })}</li>
            )}
            {result.tags_upserted > 0 && <li>{t('importArchive.tagsCreated', { count: result.tags_upserted })}</li>}
            {result.correspondents_upserted > 0 && (
              <li>{t('importArchive.correspondentsCreated', { count: result.correspondents_upserted })}</li>
            )}
            {result.document_types_upserted > 0 && (
              <li>{t('importArchive.typesCreated', { count: result.document_types_upserted })}</li>
            )}
            {result.failed > 0 && <li>{t('importArchive.documentsFailed', { count: result.failed })}</li>}
          </ul>
          {result.errors.length > 0 && (
            <ul className="flex flex-col gap-1 text-sm text-madder">
              {result.errors.map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          )}
          <Link to="/" className="text-sm font-medium text-oxblood underline">
            {t('uploadScan.openDocuments')}
          </Link>
        </div>
      )}

      {error && <p className="text-sm text-madder">{error}</p>}
    </section>
  )
}
