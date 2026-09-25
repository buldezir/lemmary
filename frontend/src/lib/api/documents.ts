import { pb, pbUrl } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch, errorDetail } from '../apiClient'
import { formatBytes } from './limits'
import { bindingBody, type ProviderBinding } from './providers'
import { notifyDocumentsChanged } from '../documentEvents'
import { UNFINISHED_STATUS, type DocumentStatus } from '../documentStatus'
import type { ProcessingStep, ReprocessMode } from '../processing'
import type { TimelineMonth } from '../timeline'

// Re-exported from ./tags because a document's expand carries them.
export type { TagRecord } from './tags'
import type { TagRecord } from './tags'
import type { DocumentOwner } from '../documentQuery'

export type DocumentTypeRecord = {
  id: string
  name: string
  name_original: string
  user?: string
}

export type CorrespondentRecord = {
  id: string
  name: string
  name_original: string
  user?: string
}

export type DocumentRecord = {
  id: string
  collectionId: string
  collectionName: string
  created: string
  updated: string
  file: string
  user: string
  title: string
  title_original: string
  purpose: string
  purpose_original: string
  document_date: string
  document_type: string
  correspondent: string
  ocr_text: string
  summary: string
  summary_original: string
  processing_status: DocumentStatus
  metadata_source: string
  confidence: number
  people_or_organizations: string[]
  tags: string[]
  checksum?: string
  text_fingerprint?: string
  duplicate_of?: string
  expand?: {
    tags?: TagRecord[]
    document_type?: DocumentTypeRecord
    correspondent?: CorrespondentRecord
    duplicate_of?: DocumentRecord
  }
}

// Narrower than DocumentRecord so a caller does not depend on a record the
// detail page replaces once a second while the pipeline runs.
export type DocumentFileRef = Pick<DocumentRecord, 'id' | 'collectionId' | 'file'>

// The file fields are protected: a bare URL is refused, access needs a
// short-lived token minted for the signed-in user.
//
// requestKey: null — two concurrent mints on the one token path would
// auto-cancel each other, and the preview pane mints while the user clicks
// Open file.
export async function fileUrlWithToken(record: DocumentFileRef, filename?: string) {
  const name = filename ?? record.file
  // getURL answers "" for an empty filename, and fetching "" resolves against
  // index.html with a 200, which no response check would catch.
  if (!name) {
    throw new Error('This document has no file.')
  }

  await ensureAuth()
  const token = await pb.files.getToken({ requestKey: null })
  return pb.files.getURL(record, name, { token })
}

// The tab is opened synchronously in the click handler and pointed at the
// tokened URL later: a window.open after an await is eaten by popup blockers.
export async function openDocumentFile(record: DocumentFileRef, filename?: string) {
  const tab = window.open('', '_blank')
  try {
    const url = await fileUrlWithToken(record, filename)
    if (tab) {
      tab.opener = null
      tab.location.replace(url)
    } else {
      window.location.assign(url)
    }
  } catch (err) {
    tab?.close()
    throw err
  }
}

/** The documents.file MaxSize, see models.MaxFileBytes: 47 MiB, just under Mistral OCR's 50 MB. */
export const MAX_FILE_BYTES = 47 * 1024 * 1024

/** Above this a file still uploads, but OCR providers and the worker get slow or fail. */
export const PROCESSING_WARN_BYTES = 20 * 1024 * 1024

export const largeFileWarning =
  `Over ${formatBytes(PROCESSING_WARN_BYTES)}. Large files may fail OCR or take much longer to process.`

export function fileTooLargeMessage(sizeBytes: number): string {
  return `This file is ${formatBytes(sizeBytes)}, over the ${formatBytes(MAX_FILE_BYTES)} limit for a single document.`
}

/**
 * What to show beside a file whose upload was refused.
 *
 * PocketBase answers a field rejection with the generic "Failed to create
 * record." on top and the reason underneath, per field, so the field message
 * wins when there is one. The size code is reworded because its text quotes
 * the limit in raw bytes.
 */
export function uploadErrorMessage(err: unknown, sizeBytes = 0): string {
  if (err && typeof err === 'object') {
    const { message, response } = err as {
      message?: string
      response?: { message?: string; data?: Record<string, { code?: string; message?: string }> }
    }
    // Only a field validation failure hides its reason under the generic text.
    // A hook's own message (a duplicate, a limit) is already the reason, and
    // PocketBase rewrites whatever data it carries to "Invalid value.".
    if (response?.message === 'Failed to create record.') {
      for (const field of Object.values(response.data ?? {})) {
        if (field?.code === 'validation_file_size_limit') return fileTooLargeMessage(sizeBytes)
        if (field?.message) return field.message
      }
    }
    if (response?.message) return response.message
    if (typeof message === 'string' && message) return message
  }
  if (err instanceof Error) return err.message
  return 'Upload failed'
}

export function parseDuplicateOfId(message: string): string | null {
  const match = message.match(/duplicate of ([a-z0-9]{15})/i)
  return match?.[1] ?? null
}

export const SHARED_TAG_NAME = 'shared'

export type DocumentListFilters = {
  status: string
  documentType: string
  correspondent: string
  dateFrom: string
  dateTo: string
  undated?: boolean
  /** tags ids a document must carry all of. */
  tags?: string[]
  untagged?: boolean
  owner?: DocumentOwner
}

/**
 * The day after a "YYYY-MM-DD" date, so an inclusive To can be asked as an
 * exclusive `<`. UTC, matching how document_date is stored.
 */
function dayAfter(date: string): string {
  const parsed = new Date(`${date}T00:00:00Z`)
  if (Number.isNaN(parsed.getTime())) return date
  parsed.setUTCDate(parsed.getUTCDate() + 1)
  return parsed.toISOString().slice(0, 10)
}

export function buildDocumentFilter(filters: DocumentListFilters): string | undefined {
  const parts: string[] = []

  if (filters.status === UNFINISHED_STATUS) {
    parts.push(pb.filter('processing_status != {:status}', { status: 'completed' }))
  } else if (filters.status !== 'all') {
    parts.push(pb.filter('processing_status = {:status}', { status: filters.status }))
  }
  if (filters.documentType !== 'all') {
    parts.push(pb.filter('document_type = {:id}', { id: filters.documentType }))
  }
  if (filters.correspondent !== 'all') {
    parts.push(pb.filter('correspondent = {:id}', { id: filters.correspondent }))
  }
  if (filters.dateFrom) {
    parts.push(pb.filter('document_date >= {:date}', { date: filters.dateFrom }))
  }
  if (filters.dateTo) {
    // Exclusive next-day bound: document_date is stored as
    // "YYYY-MM-DD HH:MM:SS.sssZ" and compared as a string, so `<= dateTo`
    // dropped every document dated on the To day. Search bounds it the same
    // way (fulltext.parseDayBoundary).
    parts.push(pb.filter('document_date < {:date}', { date: dayAfter(filters.dateTo) }))
  }
  if (filters.undated) {
    // PocketBase compares an empty literal null-safely, so this covers both
    // the empty string and a null date.
    parts.push("document_date = ''")
  }
  // ponytail: substring match on the stored id array, because PocketBase
  // cannot express "has all of these" -- two `tags.id ?=` clauses reuse one
  // join alias and match nothing. The needle carries the JSON quotes around
  // the id so it cannot match inside a longer one. Move the unsearched list
  // onto a Go endpoint (ngxapi's tagsExpr) if this stops paying.
  for (const tag of filters.tags ?? []) {
    parts.push(pb.filter('tags ~ {:id}', { id: `"${tag}"` }))
  }
  if (filters.untagged) {
    parts.push('tags:length = 0')
  }
  const me = pb.authStore.record?.id ?? ''
  if (filters.owner === 'mine' && me) {
    parts.push(pb.filter('user = {:me}', { me }))
  } else if (filters.owner === 'shared' && me) {
    parts.push(pb.filter('user != {:me}', { me }))
  }

  return parts.length > 0 ? parts.join(' && ') : undefined
}

export async function reprocessDocument(
  documentId: string,
  steps: ProcessingStep[],
  forceSteps?: ProcessingStep[],
  overrides?: JobOverrides,
) {
  await ensureAuth()
  await pb.collection('documents').update(documentId, {
    processing_status: 'pending',
  })
  return pb.collection('processing_jobs').create({
    document: documentId,
    status: 'pending',
    steps,
    ...(forceSteps?.length ? { force_steps: forceSteps } : {}),
    // The provider ids are checked by the processing_jobs create hook, not
    // here. Narrowed to the queued steps because the hook validates every
    // binding on the job, so an override left behind by an unticked step
    // would refuse the whole job.
    ...jobOverridesBody(overridesForSteps(overrides, steps)),
  })
}

// requestKey: null — polled alongside the job counts and must not auto-cancel
// a request already in flight. Counted through the documents collection, so it
// only covers the caller's own documents.
export async function countDocumentsWithStatus(
  status: DocumentStatus | typeof UNFINISHED_STATUS,
): Promise<number> {
  await ensureAuth()

  const filter =
    buildDocumentFilter({
      status,
      documentType: 'all',
      correspondent: 'all',
      dateFrom: '',
      dateTo: '',
      // Counts the same set the Inbox lists, which is the caller's own work.
      owner: 'mine',
    }) ?? ''

  const result = await pb.collection('documents').getList(1, 1, { filter, requestKey: null })
  return result.totalItems
}

export function countFailedDocuments(): Promise<number> {
  return countDocumentsWithStatus('failed')
}

/**
 * Counted over the same set the Inbox lists. Counting only needs_review would
 * leave the badge saying three while the list showed seven.
 */
export function countInboxDocuments(): Promise<number> {
  return countDocumentsWithStatus(UNFINISHED_STATUS)
}

/** allSettled: one document already gone in another tab must not discard the rest. */
export async function deleteDocuments(documentIds: string[]): Promise<void> {
  if (documentIds.length === 0) return
  await ensureAuth()

  const results = await Promise.allSettled(
    documentIds.map((id) => pb.collection('documents').delete(id, { requestKey: null })),
  )
  notifyDocumentsChanged()

  const failed = results.filter((result) => result.status === 'rejected').length
  if (failed > 0) {
    throw new Error(
      failed === documentIds.length
        ? 'Could not delete.'
        : `Deleted ${documentIds.length - failed}; ${failed} failed.`,
    )
  }
}

/**
 * A status write and nothing else: reviewing is not an edit, so metadata_source
 * still records that the model wrote the metadata. allSettled so one document
 * deleted in another tab does not discard the rest.
 */
async function setDocumentsStatus(
  documentIds: string[],
  status: 'completed' | 'needs_review',
  allFailed: string,
  someFailed: (done: number, failed: number) => string,
): Promise<void> {
  if (documentIds.length === 0) return
  await ensureAuth()

  const results = await Promise.allSettled(
    documentIds.map((id) =>
      pb.collection('documents').update(id, { processing_status: status }, { requestKey: null }),
    ),
  )
  notifyDocumentsChanged()

  const failed = results.filter((result) => result.status === 'rejected').length
  if (failed > 0) {
    throw new Error(
      failed === documentIds.length ? allFailed : someFailed(documentIds.length - failed, failed),
    )
  }
}

export function markDocumentsReviewed(documentIds: string[]): Promise<void> {
  return setDocumentsStatus(
    documentIds,
    'completed',
    'Could not mark as reviewed.',
    (done, failed) => `Marked ${done} reviewed; ${failed} failed.`,
  )
}

export function markDocumentsUnreviewed(documentIds: string[]): Promise<void> {
  return setDocumentsStatus(
    documentIds,
    'needs_review',
    'Could not mark unreviewed.',
    (done, failed) => `Marked ${done} unreviewed; ${failed} failed.`,
  )
}

export type ReprocessResult = {
  queued: number
  skipped: number
  remaining: number
}

/**
 * Mirrors config.Overrides, minus the chat and search bindings a job never uses.
 *
 * An embedding override must name the model already bound in Settings: the
 * retrieval index only reads chunk rows matching the configured model, so the
 * server refuses anything else. Vectors nothing will ever read look like success.
 */
export type JobOverrides = {
  ocr?: ProviderBinding
  extract?: ProviderBinding
  embedding?: ProviderBinding
}

/**
 * The pipeline steps that call a provider, and the binding each one uses. One
 * table, so the pickers and the request cannot disagree.
 */
export const STEP_BINDINGS = {
  ocr: 'ocr',
  extract_metadata: 'extract',
  embed: 'embedding',
} as const satisfies Partial<Record<ProcessingStep, keyof JobOverrides>>

export function overridesForSteps(
  overrides: JobOverrides | undefined,
  steps: ProcessingStep[],
): JobOverrides {
  const out: JobOverrides = {}
  for (const step of steps) {
    const key = STEP_BINDINGS[step as keyof typeof STEP_BINDINGS]
    const binding = key && overrides?.[key]
    if (key && binding) {
      out[key] = binding
    }
  }
  return out
}

export function jobOverridesBody(overrides: JobOverrides | undefined) {
  if (!overrides) return {}
  const body: Record<string, ProviderBinding> = {}
  for (const [name, binding] of Object.entries(overrides)) {
    const fields = bindingBody(binding)
    if ('provider_id' in fields) {
      body[name] = fields as ProviderBinding
    }
  }
  return Object.keys(body).length > 0 ? { overrides: body } : {}
}

/** One line naming the overridden bindings for the confirm dialogs, or "". */
export function describeJobOverrides(overrides: JobOverrides | undefined): string {
  // Read off the input: jobOverridesBody's output is "the field or nothing".
  return Object.entries(overrides ?? {})
    .filter(([, binding]) => binding?.provider_id.trim())
    .map(([name, binding]) => `${name}: ${binding!.model.trim() || 'provider default'}`)
    .join(', ')
}

function postReprocess(body: Record<string, unknown>) {
  return apiFetch<ReprocessResult>('/api/app/documents/reprocess-failed', {
    method: 'POST',
    body,
    fallbackError: 'Reprocess failed',
  })
}

/** Requeues up to `limit` of the caller's failed documents, oldest first. */
export function reprocessFailedDocuments(opts: {
  limit?: number
  mode?: ReprocessMode
  overrides?: JobOverrides
}) {
  return postReprocess({
    ...(opts.limit ? { limit: opts.limit } : {}),
    mode: opts.mode ?? 'auto',
    ...jobOverridesBody(opts.overrides),
  })
}

/** Documents already queued are skipped, so a stale selection cannot double-queue. */
export function reprocessDocuments(
  documentIds: string[],
  mode: ReprocessMode = 'auto',
  overrides?: JobOverrides,
) {
  return postReprocess({ document_ids: documentIds, mode, ...jobOverridesBody(overrides) })
}

export type DocumentSearchList = {
  page: number
  perPage: number
  totalItems: number
  totalPages: number
  items: DocumentRecord[]
}

export async function searchDocuments(opts: {
  q: string
  page: number
  perPage: number
  status?: string
  documentType?: string
  correspondent?: string
  dateFrom?: string
  dateTo?: string
  undated?: boolean
  tags?: string[]
  untagged?: boolean
  owner?: DocumentOwner
}): Promise<DocumentSearchList> {
  const params = new URLSearchParams()
  params.set('q', opts.q)
  params.set('page', String(opts.page))
  params.set('perPage', String(opts.perPage))
  if (opts.status && opts.status !== 'all') {
    params.set('status', opts.status)
  }
  if (opts.documentType && opts.documentType !== 'all') {
    params.set('document_type', opts.documentType)
  }
  if (opts.correspondent && opts.correspondent !== 'all') {
    params.set('correspondent', opts.correspondent)
  }
  if (opts.dateFrom) {
    params.set('date_from', opts.dateFrom)
  }
  if (opts.dateTo) {
    params.set('date_to', opts.dateTo)
  }
  if (opts.undated) {
    params.set('undated', 'true')
  }
  if (opts.tags?.length) {
    params.set('tags', opts.tags.join(','))
  }
  if (opts.owner && opts.owner !== 'all') {
    params.set('owner', opts.owner)
  }
  if (opts.untagged) {
    params.set('untagged', 'true')
  }

  const data = await apiFetch<Partial<DocumentSearchList>>(
    `/api/app/documents/search?${params}`,
    { fallbackError: 'Failed to search documents' },
  )
  return {
    page: data.page ?? opts.page,
    perPage: data.perPage ?? opts.perPage,
    totalItems: data.totalItems ?? 0,
    totalPages: data.totalPages ?? 0,
    items: data.items ?? [],
  }
}

/** Every id the list's filters match, across all of its pages. */
export async function listMatchingDocumentIds(
  q: string,
  filters: DocumentListFilters,
): Promise<string[]> {
  if (!q) {
    await ensureAuth()
    const filter = buildDocumentFilter(filters)
    const records = await pb.collection('documents').getFullList<{ id: string }>({
      fields: 'id',
      // A unique order, or the batches past the first are an unordered OFFSET.
      sort: 'id',
      // requestKey: null -- the list's own getList shares the default key, so
      // either would auto-cancel the other.
      requestKey: null,
      ...(filter ? { filter } : {}),
    })
    return records.map((record) => record.id)
  }
  const ids: string[] = []
  for (let page = 1; ; page++) {
    const result = await searchDocuments({ ...filters, q, page, perPage: 100 })
    ids.push(...result.items.map((document) => document.id))
    if (page >= result.totalPages) return ids
  }
}

export type DocumentTimeline = {
  months: TimelineMonth[]
  /** Only the undated filter reaches these. */
  undated: number
}

/**
 * Whole-library counts per calendar month, newest first: deliberately ignores
 * the list's filters, so the timeline is a stable map of the archive.
 */
export async function fetchDocumentTimeline(): Promise<DocumentTimeline> {
  const data = await apiFetch<Partial<DocumentTimeline>>('/api/app/documents/timeline', {
    fallbackError: 'Failed to load the timeline',
  })
  return {
    months: data.months ?? [],
    undated: data.undated ?? 0,
  }
}

type TaxonomyCollection = 'document_types' | 'correspondents'

// requestKey: null — several upserts run concurrently and must not auto-cancel
// each other.
//
// Tags are deliberately not a TaxonomyCollection: they are a vocabulary the
// user curates on the Tags page, so nothing here may conjure a new one.
async function upsertTaxonomyByName(
  collection: TaxonomyCollection,
  name: string,
  userId: string,
  extra: Record<string, unknown> = {},
): Promise<string> {
  const existing = await pb.collection(collection).getList(1, 1, {
    filter: pb.filter('name = {:name}', { name }),
    requestKey: null,
  })
  if (existing.items.length > 0) {
    return existing.items[0].id
  }
  const created = await pb
    .collection(collection)
    .create({ name, user: userId, ...extra }, { requestKey: null })
  return created.id
}

export type DocumentMetadataInput = {
  title: string
  purpose: string
  summary: string
  /**
   * Editable because everything else is derived from it, so a misread total is
   * worth fixing at the source. Saving marks the document's vectors stale
   * (embedstore's staleFields), so the backfill re-embeds from the correction.
   */
  ocrText: string
  documentDate: string
  documentTypeName: string
  correspondentName: string
  /** Ids from the user's existing tags; saving never creates one. */
  tagIds: string[]
  processingStatus: DocumentRecord['processing_status']
}

export async function saveDocumentMetadata(
  documentId: string,
  input: DocumentMetadataInput,
): Promise<DocumentRecord> {
  await ensureAuth()
  const userId = pb.authStore.record?.id
  if (!userId) {
    throw new Error('You must be signed in to save metadata.')
  }

  const tagIds = [...new Set(input.tagIds.filter(Boolean))]
  const documentTypeName = input.documentTypeName.trim()
  const correspondentName = input.correspondentName.trim()

  const [documentTypeId, correspondentId] = await Promise.all([
    documentTypeName
      ? upsertTaxonomyByName('document_types', documentTypeName, userId, {
          name_original: documentTypeName,
        })
      : Promise.resolve(''),
    correspondentName
      ? upsertTaxonomyByName('correspondents', correspondentName, userId, {
          name_original: correspondentName,
        })
      : Promise.resolve(''),
  ])

  const saved = await pb.collection('documents').update<DocumentRecord>(documentId, {
    title: input.title,
    purpose: input.purpose,
    summary: input.summary,
    ocr_text: input.ocrText,
    document_date: input.documentDate || null,
    document_type: documentTypeId || null,
    correspondent: correspondentId || null,
    tags: tagIds,
    metadata_source: 'user',
    processing_status:
      input.processingStatus === 'needs_review' ? 'completed' : input.processingStatus,
  })
  notifyDocumentsChanged()
  return saved
}

/** The OCR text in the result language: translated and stored on first request, or again with `force`. */
export async function translateOcrText(documentId: string, force = false): Promise<string> {
  const data = await apiFetch<{ text?: string }>(
    `/api/app/documents/${encodeURIComponent(documentId)}/translation${force ? '?force=1' : ''}`,
    { method: 'POST', fallbackError: 'Translation failed' },
  )
  return data.text ?? ''
}

/**
 * The backup archive as a blob: every document with its OCR text, metadata and
 * thumbnail plus the taxonomy, as Import -> Lemmary archive restores from.
 * Given ids, only those of them the caller can read.
 */
export async function fetchDocumentsArchive(ids?: string[]): Promise<Blob> {
  await ensureAuth()

  const response = await fetch(`${pbUrl}/api/app/documents/export`, {
    method: 'POST',
    headers: {
      Authorization: pb.authStore.token,
      ...(ids ? { 'Content-Type': 'application/json' } : {}),
    },
    ...(ids ? { body: JSON.stringify({ ids }) } : {}),
  })

  if (!response.ok) {
    let data: unknown = null
    try {
      data = await response.json()
    } catch {
      // response may be non-JSON on some errors
    }
    throw new Error(errorDetail(data, 'Failed to download archive'))
  }

  return response.blob()
}
