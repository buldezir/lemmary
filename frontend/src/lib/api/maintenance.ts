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
 * The embedding backfill's state. `started` says whether this call is what
 * started the sweep; `running` says whether one is in flight either way, so a
 * click that lands on an already-running sweep reads as "still going" rather
 * than as a failure.
 */
export type EmbeddingBackfillState = {
  started: boolean
  running: boolean
  stats: EmbeddingStats
}

/**
 * Starts a sweep over every document that still needs embedding. It returns as
 * soon as the sweep is queued — progress comes from polling
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
function countJobsByStatus(status: string) {
  return pb
    .collection('processing_jobs')
    .getList(1, 1, { filter: pb.filter('status = {:status}', { status }), requestKey: null })
    .then((result) => result.totalItems)
}

// Counted through the processing_jobs collection, so it only covers jobs on the
// caller's own documents (that collection's list rule is document.user = auth.id).
export async function getActiveJobCounts(): Promise<ActiveJobCounts> {
  await ensureAuth()

  const [pending, running] = await Promise.all([
    countJobsByStatus('pending'),
    countJobsByStatus('running'),
  ])

  return { pending, running }
}
