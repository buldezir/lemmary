import { apiFetch, pollJob, type JobProgress } from '../apiClient'

export type NgxImportMode = 'preserve' | 'reprocess'

export type NgxImportResult = {
  imported: number
  skipped_duplicates: number
  failed: number
  tags_upserted: number
  correspondents_upserted: number
  document_types_upserted: number
  errors: string[]
}

export async function importFromNgx(
  url: string,
  apiKey: string,
  mode: NgxImportMode = 'preserve',
): Promise<NgxImportResult> {
  const start = await apiFetch<{ job_id?: string }>('/api/app/import/ngx', {
    method: 'POST',
    body: { url, api_key: apiKey, mode },
    fallbackError: 'Import failed to start',
  })
  if (!start.job_id) {
    throw new Error('Import job id missing from server response')
  }

  const result = await pollJob<NgxImportResult>(
    `/api/app/import/ngx/status?job_id=${encodeURIComponent(start.job_id)}`,
    { label: 'import' },
  )
  return { ...result, errors: result.errors ?? [] }
}

/**
 * Which zip entries the server counts as documents. An Amazon export is mostly
 * CSV reports and delivery photos, so that flow takes the invoice PDFs only; a
 * zip the user packed themselves takes every type the library can store. It is
 * the only difference between the two, and the staged upload remembers it, so
 * confirm and status are the same call either way.
 */
export type ZipImportSource = 'amazon' | 'zip'

const base = (source: ZipImportSource) => `/api/app/import/${source}`

export type ZipArchiveEntry = {
  path: string
  name: string
  size: number
  duplicate: boolean
  duplicate_of?: string
  oversized: boolean
}

export type ZipArchivePreview = {
  upload_id: string
  file_name: string
  expires_at: string
  /** Every importable file in the archive, duplicates included. */
  file_count: number
  /** How many of those would become new documents. */
  importable_count: number
  duplicate_count: number
  oversized_count: number
  /** Entries this source does not import, skipped. */
  ignored_count: number
  files: ZipArchiveEntry[]
}

export type ZipImportResult = {
  imported: number
  skipped_duplicates: number
  skipped_oversized: number
  failed: number
  errors: string[]
}

export type ZipImportProgress = JobProgress

/** Stages an archive and reports what it holds. Imports nothing. */
export function uploadZipArchive(source: ZipImportSource, file: File) {
  const formData = new FormData()
  formData.append('file', file)

  return apiFetch<ZipArchivePreview>(`${base(source)}/upload`, {
    method: 'POST',
    formData,
    fallbackError: 'Failed to read the archive',
  })
}

/** Drops a staged archive the user chose not to import. */
export async function discardZipArchive(source: ZipImportSource, uploadId: string) {
  await apiFetch<unknown>(
    `${base(source)}/upload?upload_id=${encodeURIComponent(uploadId)}`,
    {
      method: 'DELETE',
      fallbackError: 'Failed to discard the archive',
    },
  )
}

/** Imports the confirmed archive, reporting progress until the job finishes. */
export async function importZipArchive(
  source: ZipImportSource,
  uploadId: string,
  onProgress?: (progress: ZipImportProgress) => void,
): Promise<ZipImportResult> {
  const start = await apiFetch<{ job_id?: string }>(base(source), {
    method: 'POST',
    body: { upload_id: uploadId },
    fallbackError: 'Import failed to start',
  })
  if (!start.job_id) {
    throw new Error('Import job id missing from server response')
  }

  const result = await pollJob<ZipImportResult>(
    `${base(source)}/status?job_id=${encodeURIComponent(start.job_id)}`,
    { onProgress, label: 'import' },
  )
  return { ...result, errors: result.errors ?? [] }
}

export type ArchiveImportMode = 'restore' | 'reprocess'

export type ArchiveEntry = {
  /** The id the document had in the instance the archive came from. */
  document_id: string
  title: string
  path: string
  name: string
  size: number
  duplicate: boolean
  duplicate_of?: string
  oversized: boolean
  /** The manifest names this file but the archive does not hold it. */
  missing: boolean
  has_ocr: boolean
  has_metadata: boolean
  has_preview: boolean
}

export type ArchivePreview = {
  upload_id: string
  file_name: string
  expires_at: string
  /** False for an archive exported before manifests existed. */
  has_manifest: boolean
  format_version: number
  /** Every document in the archive, duplicates included. */
  document_count: number
  /** How many of those would become new documents. */
  importable_count: number
  duplicate_count: number
  oversized_count: number
  missing_count: number
  /** Entries in the archive that belong to no document. */
  ignored_count: number
  /** Tags, correspondents and document types the archive restores. */
  taxonomy_count: number
  files: ArchiveEntry[]
}

export type ArchiveImportResult = {
  imported: number
  skipped_duplicates: number
  skipped_oversized: number
  failed: number
  tags_upserted: number
  correspondents_upserted: number
  document_types_upserted: number
  errors: string[]
}

export type ArchiveImportProgress = JobProgress

/** Stages a Lemmary backup archive and reports what it holds. Imports nothing. */
export function uploadArchive(file: File) {
  const formData = new FormData()
  formData.append('file', file)

  return apiFetch<ArchivePreview>('/api/app/import/archive/upload', {
    method: 'POST',
    formData,
    fallbackError: 'Failed to read the archive',
  })
}

/** Drops a staged archive the user chose not to restore. */
export async function discardArchive(uploadId: string) {
  await apiFetch<unknown>(`/api/app/import/archive/upload?upload_id=${encodeURIComponent(uploadId)}`, {
    method: 'DELETE',
    fallbackError: 'Failed to discard the archive',
  })
}

/** Restores the confirmed archive, reporting progress until the job finishes. */
export async function importArchive(
  uploadId: string,
  mode: ArchiveImportMode = 'restore',
  onProgress?: (progress: ArchiveImportProgress) => void,
): Promise<ArchiveImportResult> {
  const start = await apiFetch<{ job_id?: string }>('/api/app/import/archive', {
    method: 'POST',
    body: { upload_id: uploadId, mode },
    fallbackError: 'Import failed to start',
  })
  if (!start.job_id) {
    throw new Error('Import job id missing from server response')
  }

  const result = await pollJob<ArchiveImportResult>(
    `/api/app/import/archive/status?job_id=${encodeURIComponent(start.job_id)}`,
    { onProgress, label: 'import' },
  )
  return { ...result, errors: result.errors ?? [] }
}
