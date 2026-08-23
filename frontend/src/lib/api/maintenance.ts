import { pb } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch } from '../apiClient'

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
