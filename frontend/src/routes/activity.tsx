import { useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useAsync } from '../hooks/useAsync'
import { pb } from '../lib/pb'
import { listActiveJobs } from '../lib/api/jobs'
import { countDocumentsWithStatus, reprocessDocuments } from '../lib/api/documents'
import { discardUnprocessedDocuments, stopQueue } from '../lib/api/maintenance'
import {
  countLabel,
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
  const cancelled = jobs.filter((job) => job.finished_at && job.status === 'cancelled')
  const failed = jobs.filter((job) => job.finished_at && job.status !== 'cancelled')

  // The way out of a mistaken import: stop what is queued, then throw away what
  // it was queued for. Both live here rather than on Management, which is admin
  // only -- the person who has just dropped four hundred documents in by
  // accident is watching this page.
  const [busy, setBusy] = useState('')
  const [notice, setNotice] = useState('')
  const [actionError, setActionError] = useState('')

  async function onStopAll() {
    setBusy('stop')
    setNotice('')
    setActionError('')
    try {
      const result = await stopQueue()
      setNotice(
        result.stopped === 0
          ? result.remaining > 0
            ? `${countLabel(result.remaining, 'queued job', 'queued jobs')} could not be stopped.`
            : 'Nothing was queued to stop.'
          : `Stopped ${countLabel(result.stopped, 'queued job', 'queued jobs')}.` +
              (result.running > 0 ? ' The document already being processed finishes.' : '') +
              (result.remaining > 0
                ? ` ${countLabel(result.remaining, 'queued job', 'queued jobs')} could not be stopped.`
                : '') +
              ' They are listed as cancelled, and Reprocess queues them again.' +
              ' An import still unpacking keeps adding to the queue -- stop again once it has finished.',
      )
      await reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Could not stop the queue')
    } finally {
      setBusy('')
    }
  }

  // Counted on the click rather than polled: the number only matters at the
  // moment it goes into the confirmation, and this page already polls enough.
  async function onDiscard() {
    setBusy('discard')
    setNotice('')
    setActionError('')
    try {
      const [queued, cancelledCount] = await Promise.all([
        countDocumentsWithStatus('pending'),
        countDocumentsWithStatus('cancelled'),
      ])
      const total = queued + cancelledCount
      if (total === 0) {
        setNotice('Nothing unprocessed to delete.')
        return
      }
      const confirmed = window.confirm(
        `Delete up to ${countLabel(total, 'unprocessed document', 'unprocessed documents')} ` +
          `(${queued} queued, ${cancelledCount} cancelled)?\n\n` +
          'The original files go too. Failed documents are not touched, and neither is a ' +
          'document that has already been processed once and is only queued again. This cannot be undone.',
      )
      if (!confirmed) return
      const result = await discardUnprocessedDocuments()
      setNotice(
        `Deleted ${countLabel(result.deleted, 'document', 'documents')}.` +
          (result.kept > 0
            ? ` ${countLabel(result.kept, 'document was', 'documents were')} kept: already processed, only queued again.`
            : '') +
          (result.remaining > 0 ? ` ${result.remaining} could not be deleted.` : ''),
      )
      await reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Could not delete the unprocessed documents')
    } finally {
      setBusy('')
    }
  }

  return (
    <section className={sectionClassName}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h2 className={sectionTitleClassName}>Activity</h2>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="secondary"
            size="xs"
            disabled={busy !== ''}
            onClick={() => void onStopAll()}
          >
            {busy === 'stop' ? 'Stopping...' : 'Stop all'}
          </Button>
          <Button
            variant="secondary"
            size="xs"
            disabled={busy !== ''}
            onClick={() => void onDiscard()}
          >
            {busy === 'discard' ? 'Deleting...' : 'Delete unprocessed'}
          </Button>
        </div>
      </div>
      <p className="mb-4 text-sm text-ink-soft">
        What the pipeline is doing to your documents, and what stopped or went wrong. Recent
        cancellations and failures stay listed so terminal work is still here to be found.
      </p>

      {notice ? <p className="mb-3 text-sm text-ink-soft">{notice}</p> : null}
      {actionError ? <p className="mb-3 text-sm text-madder">{actionError}</p> : null}
      {error ? <p className="mb-3 text-sm text-madder">{error}</p> : null}

      {loading && jobs.length === 0 ? (
        <p className="text-sm text-ink-soft">Loading the queue...</p>
      ) : jobs.length === 0 ? (
        <p className="text-sm text-ink-soft">
          Nothing in the queue, and nothing was cancelled or failed in the last day.
        </p>
      ) : (
        <div className="flex flex-col gap-6">
          <JobGroup title="In progress" jobs={active} tick={tick} onReprocessed={reload} />
          <JobGroup title="Recently cancelled" jobs={cancelled} tick={tick} onReprocessed={reload} />
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
