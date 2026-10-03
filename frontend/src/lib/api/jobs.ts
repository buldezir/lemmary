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

/** Tells a job waiting behind a long queue from one no worker will pick up. */
export async function anyJobRunning(): Promise<boolean> {
  await ensureAuth()
  const jobs = await pb.collection(collection).getList(1, 1, {
    filter: "started_at != '' && finished_at = ''",
    fields: 'id',
    skipTotal: true,
    requestKey: null,
  })
  return jobs.items.length > 0
}

/** How far back a finished-and-failed job stays on the Activity page. */
const failedWindowMs = 24 * 60 * 60_000

/**
 * Everything unfinished, plus failures and cancellations from the last day
 * that still stand. finished_at = '' rather than a status test, for the reason
 * createProcessingJob gives. Oldest first, the order the worker takes them in,
 * so the job running now heads the list.
 */
export async function listActiveJobs(
  limit = 100,
): Promise<{ jobs: ProcessingJobRecord[]; total: number }> {
  await ensureAuth()
  const since = new Date(Date.now() - failedWindowMs).toISOString().replace('T', ' ')
  // finished_at, not created: a job that ran for two days and then failed is a
  // failure from a minute ago. The document's status, because a reprocess
  // leaves the failed job behind: once the document has moved on, so has the
  // failure.
  const filter = `finished_at = '' || ${pb.filter(
    '(((status = "failed" && document.processing_status = "failed") || (status = "cancelled" && document.processing_status = "cancelled")) && finished_at >= {:since})',
    { since },
  )}`

  const result = await pb.collection(collection).getList<ProcessingJobRecord>(1, limit, {
    filter,
    sort: 'created',
    expand: 'document',
    requestKey: null,
  })

  // A document that failed twice has two failed jobs; the newest says why.
  const newest = new Map<string, ProcessingJobRecord>()
  for (const job of result.items) {
    if (job.finished_at) newest.set(job.document, job)
  }
  const jobs = result.items.filter((job) => !job.finished_at || newest.get(job.document) === job)

  // The total as well as the page, so a bulk upload of 150 does not look like
  // it lost fifty behind a hundred-row page.
  return { jobs, total: result.totalItems - (result.items.length - jobs.length) }
}
