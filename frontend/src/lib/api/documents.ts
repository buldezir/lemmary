import { pb, pbUrl } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch, errorDetail } from '../apiClient'
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
  processing_status: 'pending' | 'processing' | 'completed' | 'failed' | 'needs_review'
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

// The document file fields are protected, so a bare file URL is refused by the
// server: access needs a short-lived file token minted for the signed-in user.
// A plain URL would be a bearer capability — valid for anyone holding the
// link, surviving logout for as long as the record exists.
export async function fileUrlWithToken(record: DocumentRecord, filename?: string) {
  const token = await pb.files.getToken()
  return pb.files.getURL(record, filename ?? record.file, { token })
}

// Opens a document file in a new tab. The tab is opened synchronously in the
// click handler — a window.open that happens after an await is eaten by popup
// blockers — and pointed at the tokened URL once it arrives.
export async function openDocumentFile(record: DocumentRecord, filename?: string) {
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
}

/** PocketBase filter for the document list; undefined when nothing is active. */
export function buildDocumentFilter(filters: DocumentListFilters): string | undefined {
  const parts: string[] = []

  if (filters.status !== 'all') {
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
    parts.push(pb.filter('document_date <= {:date}', { date: filters.dateTo }))
  }

  return parts.length > 0 ? parts.join(' && ') : undefined
}

export async function reprocessDocument(
  documentId: string,
  steps: ProcessingStep[],
  forceSteps?: ProcessingStep[],
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
  })
}

// requestKey: null — this is polled alongside the job counts and must not
// auto-cancel a request already in flight.
//
// Counted through the documents collection, so it only covers the caller's own
// documents (that collection's rules are user = @request.auth.id).
export async function countFailedDocuments(): Promise<number> {
  await ensureAuth()

  const result = await pb
    .collection('documents')
    .getList(1, 1, { filter: `processing_status = "failed"`, requestKey: null })
  return result.totalItems
}

export type ReprocessResult = {
  queued: number
  skipped: number
  remaining: number
}

function postReprocess(body: Record<string, unknown>) {
  return apiFetch<ReprocessResult>('/api/app/documents/reprocess-failed', {
    method: 'POST',
    body,
    fallbackError: 'Reprocess failed',
  })
}

/** Requeues up to `limit` of the caller's failed documents, oldest first. */
export function reprocessFailedDocuments(opts: { limit?: number; mode?: ReprocessMode }) {
  return postReprocess({
    ...(opts.limit ? { limit: opts.limit } : {}),
    mode: opts.mode ?? 'auto',
  })
}

/**
 * Requeues an explicit selection. Documents already queued are skipped, so a
 * stale selection cannot double-queue.
 */
export function reprocessDocuments(documentIds: string[], mode: ReprocessMode = 'auto') {
  return postReprocess({ document_ids: documentIds, mode })
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
  /** Documents with no document_date; no date range can reach them. */
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

  return pb.collection('documents').update<DocumentRecord>(documentId, {
    title: input.title,
    purpose: input.purpose,
    summary: input.summary,
    document_date: input.documentDate || null,
    document_type: documentTypeId || null,
    correspondent: correspondentId || null,
    tags: tagIds,
    metadata_source: 'user',
    processing_status:
      input.processingStatus === 'needs_review' ? 'completed' : input.processingStatus,
  })
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
