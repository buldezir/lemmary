import { pb, pbUrl } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch, errorDetail } from '../apiClient'
import { bindingBody, type ProviderBinding } from './providers'
import { notifyDocumentsChanged } from '../documentEvents'
import { UNFINISHED_STATUS, type DocumentStatus } from '../documentStatus'
import type { ProcessingStep, ReprocessMode } from '../processing'
import type { TimelineMonth } from '../timeline'

export type TagRecord = {
  id: string
  name: string
  user?: string
}

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

// The three fields a file URL is built from. Narrower than DocumentRecord so a
// caller can depend on these primitives instead of the whole record, which the
// detail page replaces once a second while the pipeline runs.
export type DocumentFileRef = Pick<DocumentRecord, 'id' | 'collectionId' | 'file'>

// The document file fields are protected, so a bare file URL is refused by the
// server: access needs a short-lived file token minted for the signed-in user.
// A plain URL would be a bearer capability — valid for anyone holding the
// link, surviving logout for as long as the record exists.
//
// requestKey: null — the token endpoint is one path, so two concurrent mints
// would otherwise auto-cancel each other and the loser would reject as an
// abort. Two at once is ordinary: the preview pane mints on mount while the
// user clicks Open file.
export async function fileUrlWithToken(record: DocumentFileRef, filename?: string) {
  const name = filename ?? record.file
  // getURL answers "" for an empty filename, and fetching "" resolves against
  // the current page — index.html, with a 200, which no response check would
  // catch. A document with no file has no URL, and says so.
  if (!name) {
    throw new Error('This document has no file.')
  }

  await ensureAuth()
  const token = await pb.files.getToken({ requestKey: null })
  return pb.files.getURL(record, name, { token })
}

// Opens a document file in a new tab. The tab is opened synchronously in the
// click handler — a window.open that happens after an await is eaten by popup
// blockers — and pointed at the tokened URL once it arrives.
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

export function parseDuplicateOfId(message: string): string | null {
  const match = message.match(/duplicate of ([a-z0-9]{15})/i)
  return match?.[1] ?? null
}

export type DocumentListFilters = {
  status: string
  documentType: string
  correspondent: string
  dateFrom: string
  dateTo: string
  /** Only the documents with no document_date. */
  undated?: boolean
}

/**
 * The day after a "YYYY-MM-DD" date, as "YYYY-MM-DD", so an inclusive To can be
 * asked as an exclusive `<`. Via UTC, matching how document_date is stored; an
 * unparseable value is handed back untouched for the filter to reject.
 */
function dayAfter(date: string): string {
  const parsed = new Date(`${date}T00:00:00Z`)
  if (Number.isNaN(parsed.getTime())) return date
  parsed.setUTCDate(parsed.getUTCDate() + 1)
  return parsed.toISOString().slice(0, 10)
}

/** PocketBase filter for the document list; undefined when nothing is active. */
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
    // Exclusive next-day bound rather than `<= dateTo`. document_date is a
    // PocketBase DateField, so it is stored as "YYYY-MM-DD HH:MM:SS.sssZ" and
    // compared as a string: "2025-03-31 00:00:00.000Z" <= "2025-03-31" is
    // false, which silently dropped every document dated on the To day. Search
    // bounds the same filter this way (fulltext.parseDayBoundary), so the two
    // paths now agree about which documents a From/To range holds.
    parts.push(pb.filter('document_date < {:date}', { date: dayAfter(filters.dateTo) }))
  }
  if (filters.undated) {
    // PocketBase compares an empty literal null-safely, so this one clause
    // covers both the empty string and a null date.
    parts.push("document_date = ''")
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
    // Written straight onto the record, like the rest of this call. The
    // provider ids are checked by the processing_jobs create hook, not here --
    // this collection is writable by the document's owner, so the server has
    // to be the one that refuses a binding it cannot serve.
    //
    // Narrowed to the steps being queued, here rather than at the caller: the
    // hook validates every binding on the job, so an override left behind by a
    // step that was ticked and then unticked would refuse the whole job.
    ...jobOverridesBody(overridesForSteps(overrides, steps)),
  })
}

// requestKey: null — these are polled alongside the job counts and must not
// auto-cancel a request already in flight.
//
// Counted through the documents collection, so it only covers the caller's own
// documents (that collection's rules are user = @request.auth.id).
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
    }) ?? ''

  const result = await pb.collection('documents').getList(1, 1, { filter, requestKey: null })
  return result.totalItems
}

export function countFailedDocuments(): Promise<number> {
  return countDocumentsWithStatus('failed')
}

/**
 * The Inbox's size, counted over the same set the Inbox lists -- everything the
 * pipeline has not finished with. Counting only needs_review would leave the
 * badge saying three while the list showed seven.
 */
export function countInboxDocuments(): Promise<number> {
  return countDocumentsWithStatus(UNFINISHED_STATUS)
}

/**
 * Deletes documents outright, files and all.
 *
 * allSettled for the same reason as markDocumentsReviewed: one document already
 * gone in another tab must not discard eleven successes. The owner DeleteRule
 * permits it directly, so there is no endpoint to go through.
 */
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
 * Clears documents out of the review Inbox.
 *
 * A status write and nothing else: reviewing is not an edit, so metadata_source
 * still records that the model wrote the metadata. The owner UpdateRule permits
 * it directly, so there is no endpoint to go through.
 *
 * allSettled rather than all, so one document deleted in another tab does not
 * discard eleven successes.
 */
export async function markDocumentsReviewed(documentIds: string[]): Promise<void> {
  if (documentIds.length === 0) return
  await ensureAuth()

  const results = await Promise.allSettled(
    documentIds.map((id) =>
      pb
        .collection('documents')
        .update(id, { processing_status: 'completed' }, { requestKey: null }),
    ),
  )
  notifyDocumentsChanged()

  const failed = results.filter((result) => result.status === 'rejected').length
  if (failed > 0) {
    throw new Error(
      failed === documentIds.length
        ? 'Could not mark as reviewed.'
        : `Marked ${documentIds.length - failed} reviewed; ${failed} failed.`,
    )
  }
}

export type ReprocessResult = {
  queued: number
  skipped: number
  remaining: number
}

/**
 * The provider and model a reprocess job runs on, instead of the bindings in
 * Settings. Mirrors config.Overrides, minus the two bindings a job never uses
 * (chat and search), which the endpoint refuses outright.
 *
 * An embedding override must name the model already bound in Settings: a chunk
 * row records the model it was produced with, and the retrieval index only
 * reads rows matching the configured one. The server refuses anything else,
 * because vectors nothing will ever read look exactly like success.
 */
export type JobOverrides = {
  ocr?: ProviderBinding
  extract?: ProviderBinding
  embedding?: ProviderBinding
}

/**
 * The request field for a set of job overrides, or nothing when none were
 * chosen -- so a reprocess with every picker untouched sends exactly what it
 * sent before overrides existed.
 *
 * Half-filled bindings are dropped rather than sent: `bindingBody` returns
 * nothing for a binding with no provider, and a key whose value is `{}` would
 * be a binding the server has to refuse.
 */
/**
 * The pipeline steps that call a provider, and the job binding each one uses.
 * The other three reach no provider. One table, so the pickers and the request
 * cannot disagree about which binding a step reads.
 */
export const STEP_BINDINGS = {
  ocr: 'ocr',
  extract_metadata: 'extract',
  embed: 'embedding',
} as const satisfies Partial<Record<ProcessingStep, keyof JobOverrides>>

/** The overrides among `overrides` that the given steps will actually read. */
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

/**
 * One line naming the overridden bindings, for the confirm dialogs, or "" when
 * there are none.
 *
 * Worth saying out loud: a batch reprocess commits provider spend, and which
 * model it is about to be spent on is the thing the picker just changed.
 */
export function describeJobOverrides(overrides: JobOverrides | undefined): string {
  // Read off the input rather than jobOverridesBody's output, whose shape is
  // deliberately "the field or nothing" and so needs unwrapping to read back.
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

/**
 * Requeues an explicit selection. Documents already queued are skipped, so a
 * stale selection cannot double-queue.
 */
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

export type DocumentTimeline = {
  months: TimelineMonth[]
  /** Documents with no document_date; only the undated filter reaches them. */
  undated: number
}

/**
 * Counts the caller's documents per calendar month, newest month first.
 *
 * Whole-library counts: they deliberately ignore the list's other filters, so
 * the timeline is a stable map of the archive rather than a readout of the
 * current query.
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

type TaxonomyCollection = 'tags' | 'document_types' | 'correspondents'

// Reuses an existing record by exact name or creates one owned by the caller.
// requestKey: null — several upserts run concurrently and must not auto-cancel
// each other.
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
   * The extracted text. Editable because OCR is the one field everything else
   * is derived from -- a misread total or date is worth fixing at the source,
   * where a re-extraction will read the correction rather than the mistake.
   * Saving it marks the document's vectors stale (embedstore's staleFields
   * lists ocr_text), so the backfill re-embeds from the corrected text.
   */
  ocrText: string
  documentDate: string
  documentTypeName: string
  correspondentName: string
  tagNames: string[]
  processingStatus: DocumentRecord['processing_status']
}

/**
 * Persists user corrections: upserts the named taxonomy records, then writes
 * the metadata onto the document. Saving counts as reviewing, so needs_review
 * flips to completed.
 */
export async function saveDocumentMetadata(
  documentId: string,
  input: DocumentMetadataInput,
): Promise<DocumentRecord> {
  await ensureAuth()
  const userId = pb.authStore.record?.id
  if (!userId) {
    throw new Error('You must be signed in to save metadata.')
  }

  const tagNames = [...new Set(input.tagNames.map((name) => name.trim()).filter(Boolean))]
  const documentTypeName = input.documentTypeName.trim()
  const correspondentName = input.correspondentName.trim()

  const [tagIds, documentTypeId, correspondentId] = await Promise.all([
    Promise.all(tagNames.map((name) => upsertTaxonomyByName('tags', name, userId))),
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

/**
 * Fetches the backup archive as a blob; the caller decides how to save it.
 * The archive holds every document with its OCR text, metadata and thumbnail,
 * plus the whole taxonomy, and is what Import -> Lemmary archive restores from.
 */
export async function fetchDocumentsArchive(): Promise<Blob> {
  await ensureAuth()

  const response = await fetch(`${pbUrl}/api/app/documents/export`, {
    headers: { Authorization: pb.authStore.token },
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
