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
 * createProcessingJob gives. The queue oldest first, the order the worker takes
 * it in, so the job running now heads the list.
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
  const finishedFilter = pb.filter(
    '((status = "failed" && document.processing_status = "failed") || (status = "cancelled" && document.processing_status = "cancelled")) && finished_at >= {:since}',
    { since },
  )

  // Two lists, so a long queue cannot push the failures off the page.
  const [queued, finished] = await Promise.all([
    pb.collection(collection).getList<ProcessingJobRecord>(1, limit, {
      filter: "finished_at = ''",
      sort: 'created',
      expand: 'document',
      requestKey: null,
    }),
    pb.collection(collection).getList<ProcessingJobRecord>(1, limit, {
      filter: finishedFilter,
      sort: '-created',
      expand: 'document',
      requestKey: null,
    }),
  ])

  // A document that failed twice has two failed jobs, and newest first, the
  // first one met says why it is failed now.
  const seen = new Set<string>()
  const failures = finished.items.filter((job) => {
    if (seen.has(job.document)) return false
    seen.add(job.document)
    return true
  })

  // The queue's total as well as its page, so a bulk upload of 150 does not
  // look like it lost fifty behind a hundred-row page.
  return { jobs: [...queued.items, ...failures], total: queued.totalItems + failures.length }
}
