import { useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useAsync } from '../hooks/useAsync'
import { pb } from '../lib/pb'
import { listActiveJobs } from '../lib/api/jobs'
import { reprocessDocuments } from '../lib/api/documents'
import {
  formatDuration,
  jobDurationMs,
  jobStillRunning,
  summarizeJob,
  type ProcessingJobRecord,
} from '../lib/processing'
import { ProcessingStatus } from '../components/ProcessingStatus'
import { ProcessingSteps } from '../components/ProcessingSteps'
import { Button, sectionClassName, sectionTitleClassName } from '../components/ui'

// Realtime is optional everywhere in this app, so the queue also refreshes on a
// timer. Matches the Management page's interval for the same counts.
const pollMs = 5_000
const realtimeDebounceMs = 300

/**
 * The processing queue, across every document.
 *
 * The documents list answers "what do I have"; this answers "what is being done
 * to it, and what broke". It reads processing_jobs directly -- the collection's
 * list rule already scopes it to the caller's own documents -- so there is no
 * endpoint behind this page.
 */
export function ActivityPage() {
  const { data, loading, error, reload } = useAsync(() => listActiveJobs(), [])
  const jobs = data?.jobs ?? []
  const total = data?.total ?? 0

  const reloadRef = useRef(reload)
  useEffect(() => {
    reloadRef.current = reload
  })

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    function schedule() {
      clearTimeout(timer)
      timer = setTimeout(() => void reloadRef.current(), realtimeDebounceMs)
    }

    const interval = setInterval(() => void reloadRef.current(), pollMs)

    let unsubscribe: (() => void) | undefined
    let cancelled = false
    void pb
      .collection('processing_jobs')
      .subscribe('*', schedule)
      .then((off) => {
        if (cancelled) {
          void off()
          return
        }
        unsubscribe = off
      })
      .catch(() => {
        // No realtime; the interval above still carries the page.
      })

    return () => {
      cancelled = true
      clearTimeout(timer)
      clearInterval(interval)
      void unsubscribe?.()
    }
  }, [])

  // Drives the elapsed times, and only while something is unfinished -- a
  // settled queue must not re-render once a second for ever.
  const [tick, setTick] = useState(() => Date.now())
  const anyRunning = jobs.some((job) => jobStillRunning(job))
  useEffect(() => {
    if (!anyRunning) return
    const id = setInterval(() => setTick(Date.now()), 1000)
    return () => clearInterval(id)
  }, [anyRunning])

  const active = jobs.filter((job) => !job.finished_at)
  const failed = jobs.filter((job) => job.finished_at)

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>Activity</h2>
      <p className="mb-4 text-sm text-ink-soft">
        What the pipeline is doing to your documents, and what went wrong. Failures from the last
        day stay listed so a job that broke while nobody was watching is still here to be found.
      </p>

      {error ? <p className="mb-3 text-sm text-madder">{error}</p> : null}

      {loading && jobs.length === 0 ? (
        <p className="text-sm text-ink-soft">Loading the queue...</p>
      ) : jobs.length === 0 ? (
        <p className="text-sm text-ink-soft">
          Nothing in the queue, and nothing has failed in the last day.
        </p>
      ) : (
        <div className="flex flex-col gap-6">
          <JobGroup title="In progress" jobs={active} tick={tick} onReprocessed={reload} />
          <JobGroup title="Recently failed" jobs={failed} tick={tick} onReprocessed={reload} />
          {total > jobs.length ? (
            <p className="text-xs text-ink-soft">
              Showing the newest {jobs.length} of {total}. The rest appear as these finish.
            </p>
          ) : null}
        </div>
      )}
    </section>
  )
}

function JobGroup({
  title,
  jobs,
  tick,
  onReprocessed,
}: {
  title: string
  jobs: ProcessingJobRecord[]
  tick: number
  onReprocessed: () => Promise<void>
}) {
  if (jobs.length === 0) return null
  return (
    <div>
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-[0.14em] text-ink-soft">
        {title} ({jobs.length})
      </h3>
      <ul className="flex flex-col divide-y divide-line border-y border-line">
        {jobs.map((job) => (
          <JobRow key={job.id} job={job} tick={tick} onReprocessed={onReprocessed} />
        ))}
      </ul>
    </div>
  )
}

function JobRow({
  job,
  tick,
  onReprocessed,
}: {
  job: ProcessingJobRecord
  tick: number
  onReprocessed: () => Promise<void>
}) {
  const [requeueing, setRequeueing] = useState(false)
  const [requeueError, setRequeueError] = useState('')
  const title = job.expand?.document?.title?.trim() || 'Untitled document'
  const elapsed = jobDurationMs(job, tick)

  async function onReprocess() {
    setRequeueing(true)
    setRequeueError('')
    try {
      await reprocessDocuments([job.document])
      await onReprocessed()
    } catch (err) {
      setRequeueError(err instanceof Error ? err.message : 'Reprocess failed')
    } finally {
      setRequeueing(false)
    }
  }

  return (
    <li className="flex flex-col gap-1.5 py-3">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <Link
          to="/document/$documentId"
          params={{ documentId: job.document }}
          className="font-medium text-ink hover:text-oxblood"
        >
          {title}
        </Link>
        <div className="flex items-center gap-3">
          {elapsed !== null ? (
            <span className="text-xs tabular-nums text-ink-soft">{formatDuration(elapsed)}</span>
          ) : null}
          {job.finished_at ? (
            <Button variant="secondary" size="xs" disabled={requeueing} onClick={() => void onReprocess()}>
              {requeueing ? 'Queueing...' : 'Reprocess'}
            </Button>
          ) : null}
        </div>
      </div>

      <ProcessingStatus summary={summarizeJob(job, tick)} />
      <ProcessingSteps job={job} now={tick} collapsed />
      {requeueError ? <p className="text-xs text-madder">{requeueError}</p> : null}
    </li>
  )
}
