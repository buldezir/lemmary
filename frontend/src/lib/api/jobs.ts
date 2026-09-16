import { pb } from '../pb'
import { ensureAuth } from '../auth'
import type { ProcessingJobRecord } from '../processing'

// The collection's list rule is document.user = @request.auth.id, so every
// query here is already scoped to the caller's own documents.
const collection = 'processing_jobs'

/**
 * One request for the whole page rather than one per card. Over-fetched on
 * purpose, since reprocessing creates a fresh job each time, and reduced to the
 * newest per document here.
 */
export async function getLatestJobsFor(
  documentIds: string[],
): Promise<Map<string, ProcessingJobRecord>> {
  const byDocument = new Map<string, ProcessingJobRecord>()
  if (documentIds.length === 0) return byDocument

  await ensureAuth()
  const filter = documentIds
    .map((id) => pb.filter('document = {:id}', { id }))
    .join(' || ')

  const jobs = await pb.collection(collection).getList<ProcessingJobRecord>(1, 200, {
    filter,
    sort: '-created',
    // requestKey: null -- this must not auto-cancel the list's own requests,
    // nor a second page fetched while the first is still in flight.
    requestKey: null,
  })

  for (const job of jobs.items) {
    if (!byDocument.has(job.document)) byDocument.set(job.document, job)
  }
  return byDocument
}

/** How far back a finished-and-failed job stays on the Activity page. */
const failedWindowMs = 24 * 60 * 60_000

/**
 * Everything unfinished, plus failures and cancellations from the last day.
 * finished_at = '' rather than a status test, for the reason
 * createProcessingJob gives.
 */
export async function listActiveJobs(
  limit = 100,
): Promise<{ jobs: ProcessingJobRecord[]; total: number }> {
  await ensureAuth()
  const since = new Date(Date.now() - failedWindowMs).toISOString().replace('T', ' ')
  // finished_at, not created: a job that ran for two days and then failed is a
  // failure from a minute ago.
  const filter = `finished_at = '' || ${pb.filter('((status = "failed" || status = "cancelled") && finished_at >= {:since})', { since })}`

  const jobs = await pb.collection(collection).getList<ProcessingJobRecord>(1, limit, {
    filter,
    sort: '-created',
    expand: 'document',
    requestKey: null,
  })
  // The total as well as the page, so a bulk upload of 150 does not look like
  // it lost fifty behind a hundred-row page.
  return { jobs: jobs.items, total: jobs.totalItems }
}
