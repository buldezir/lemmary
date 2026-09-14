import { pb } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch } from '../apiClient'
import type { EmbeddingStats } from './settings'

export type DuplicateScanResult = {
  scanned: number
  checksum_backfilled: number
  exact_marked: number
  near_marked: number
  fingerprints_filled: number
}

export function scanDuplicates() {
  return apiFetch<DuplicateScanResult>('/api/app/duplicates/scan', {
    method: 'POST',
    fallbackError: 'Duplicate scan failed',
  })
}

export type SearchReindexResult = {
  indexed: number
}

export function reindexSearch() {
  return apiFetch<SearchReindexResult>('/api/app/search/reindex', {
    method: 'POST',
    fallbackError: 'Search reindex failed',
  })
}

/**
 * `started` says whether this call started the sweep; `running` says whether one
 * is in flight either way, so a click landing on a running sweep reads as
 * "still going" rather than as a failure.
 */
export type EmbeddingBackfillState = {
  started: boolean
  running: boolean
  stats: EmbeddingStats
}

/**
 * Returns as soon as the sweep is queued; progress comes from polling
 * getEmbeddingBackfillState.
 */
export function startEmbeddingBackfill() {
  return apiFetch<EmbeddingBackfillState>('/api/app/embeddings/backfill', {
    method: 'POST',
    fallbackError: 'Embedding backfill failed',
  })
}

export function getEmbeddingBackfillState() {
  return apiFetch<EmbeddingBackfillState>('/api/app/embeddings/backfill', {
    fallbackError: 'Failed to load the embedding backfill state',
  })
}

export type TaxonomyPruneResult = {
  tags: number
  correspondents: number
  document_types: number
}

export function pruneStaleTaxonomy() {
  return apiFetch<TaxonomyPruneResult>('/api/app/taxonomy/prune', {
    method: 'POST',
    fallbackError: 'Stale data cleanup failed',
  })
}

export type ActiveJobCounts = {
  pending: number
  running: number
}

// requestKey: null — the two counts run concurrently and must not auto-cancel
// each other, nor a poll that is already in flight.
function countJobs(filter: string) {
  return pb
    .collection('processing_jobs')
    .getList(1, 1, { filter, requestKey: null })
    .then((result) => result.totalItems)
}

/**
 * Both halves are bounded by finished_at = '' rather than by status:
 * apply_metadata writes "completed" onto the job before embed has run, so
 * counting by status left the header badge at zero while the Activity page
 * listed the work. Scoped to the caller's own documents by the list rule.
 */
export async function getActiveJobCounts(): Promise<ActiveJobCounts> {
  await ensureAuth()

  const [pending, running] = await Promise.all([
    countJobs(pb.filter("finished_at = '' && status = {:status}", { status: 'pending' })),
    countJobs(pb.filter("finished_at = '' && status != {:status}", { status: 'pending' })),
  ])

  return { pending, running }
}

export type StopQueueResult = {
  stopped: number
  /** What was already inside the pipeline and so ran to the end. */
  running: number
  /** Queued jobs the server could not update. */
  remaining: number
}

/**
 * The worker has no cancellation channel, so the document being worked on
 * finishes and this empties the queue behind it. Stopped documents land on
 * "cancelled", where Reprocess can pick them up.
 *
 * Only the queue as it stands: an archive still unpacking keeps enqueueing, so
 * stopping mid-import needs a second click once the unpack has finished.
 */
export function stopQueue() {
  return apiFetch<StopQueueResult>('/api/app/jobs/stop', {
    method: 'POST',
    fallbackError: 'Could not stop the queue',
  })
}

export type DiscardResult = {
  deleted: number
  /** Spared on purpose: queued, but already through the pipeline once. */
  kept: number
  /** Left behind by a delete that failed, so a partial sweep can say so. */
  remaining: number
}

/**
 * Deletes queued and cancelled documents that have never been through the
 * pipeline. Status alone cannot decide that either way: a failed reprocess may
 * belong to a document that processed fine before, and reprocess puts library
 * documents back on "pending". Those come back as `kept`.
 */
export function discardUnprocessedDocuments() {
  return apiFetch<DiscardResult>('/api/app/documents/discard-unprocessed', {
    method: 'POST',
    fallbackError: 'Could not delete the unprocessed documents',
  })
}
