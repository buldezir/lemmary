import { pb } from '../pb'
import { ensureAuth } from '../auth'
import type { ProcessingJobRecord } from '../processing'

// The collection's list rule is document.user = @request.auth.id, so every
// query here is already scoped to the caller's own documents.
const collection = 'processing_jobs'

/**
 * The newest job for each of the given documents, for a list that wants to say
 * more than the one-word status badge.
 *
 * One request for the whole page rather than one per card. Over-fetched on
 * purpose -- a page of twelve documents may have more than twelve jobs between
 * them, since reprocessing creates a fresh one each time -- and reduced to the
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
 * What the queue is doing: everything unfinished, plus the failures of the last
 * day so a job that broke while nobody was looking is still there to be found.
 *
 * finished_at = '' rather than a status test, for the reason createProcessingJob
 * gives: apply_metadata writes "completed" onto the job before embed has run.
 */
export async function listActiveJobs(
  limit = 100,
): Promise<{ jobs: ProcessingJobRecord[]; total: number }> {
  await ensureAuth()
  const since = new Date(Date.now() - failedWindowMs).toISOString().replace('T', ' ')
  // finished_at, not created: a job that ran for two days and then failed is a
  // failure from a minute ago, and keying the window on when it was queued
  // dropped exactly those off the page.
  const filter = `finished_at = '' || ${pb.filter('(status = "failed" && finished_at >= {:since})', { since })}`

  const jobs = await pb.collection(collection).getList<ProcessingJobRecord>(1, limit, {
    filter,
    sort: '-created',
    expand: 'document',
    requestKey: null,
  })
  // The total as well as the page: a bulk upload of 150 documents makes a
  // hundred rows and a header badge saying 150, and the page has to be able to
  // say which it is showing rather than look like it lost fifty.
  return { jobs: jobs.items, total: jobs.totalItems }
}
