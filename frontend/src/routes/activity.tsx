import { useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useAsync } from '../hooks/useAsync'
import { pb } from '../lib/pb'
import { listActiveJobs } from '../lib/api/jobs'
import {
  countDocumentsWithStatus,
  reprocessAllFailed,
  reprocessDocuments,
} from '../lib/api/documents'
import { discardUnprocessedDocuments, stopQueue } from '../lib/api/maintenance'
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
import { t } from '../i18n'

// Realtime is optional everywhere in this app, so the queue also refreshes on a
// timer, at the Maintenance page's interval for the same counts.
const pollMs = 5_000
const realtimeDebounceMs = 300

/**
 * The processing queue, across every document. It reads processing_jobs directly
 * -- the collection's list rule already scopes it to the caller's own documents
 * -- so there is no endpoint behind this page.
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

  // Drives the elapsed times, and only while something is unfinished: a settled
  // queue must not re-render once a second for ever.
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

  // Both live here rather than on admin-only Maintenance: the person who has just
  // dropped four hundred documents in by accident is watching this page.
  const [busy, setBusy] = useState('')
  const [notice, setNotice] = useState('')
  const [actionError, setActionError] = useState('')

  async function onStopAll() {
    setBusy('stop')
    setNotice('')
    setActionError('')
    try {
      const result = await stopQueue()
      const notStopped =
        result.remaining > 0 ? t('activity.notStopped', { count: result.remaining }) : ''
      setNotice(
        result.stopped === 0
          ? notStopped || t('activity.nothingToStop')
          : [
              t('activity.stopped', { count: result.stopped }),
              result.running > 0 ? t('activity.runningFinishes') : '',
              notStopped,
              t('activity.stoppedNote'),
            ]
              .filter(Boolean)
              .join(' '),
      )
      await reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t('activity.stopError'))
    } finally {
      setBusy('')
    }
  }

  async function onReprocessFailed() {
    setBusy('reprocess')
    setNotice('')
    setActionError('')
    try {
      setNotice(await reprocessAllFailed())
      await reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t('documents.reprocessFailed'))
    } finally {
      setBusy('')
    }
  }

  // Counted on the click rather than polled: the number only matters at the
  // moment it goes into the confirmation.
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
        setNotice(t('activity.nothingToDiscard'))
        return
      }
      const confirmed = window.confirm(
        t('activity.confirmDiscard', { count: total, queued, cancelled: cancelledCount }) +
          '\n\n' +
          t('activity.confirmDiscardNote'),
      )
      if (!confirmed) return
      const result = await discardUnprocessedDocuments()
      setNotice(
        [
          t('activity.deleted', { count: result.deleted }),
          result.kept > 0 ? t('activity.kept', { count: result.kept }) : '',
          result.remaining > 0 ? t('activity.notDeleted', { count: result.remaining }) : '',
        ]
          .filter(Boolean)
          .join(' '),
      )
      await reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t('activity.discardError'))
    } finally {
      setBusy('')
    }
  }

  return (
    <section className={sectionClassName}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h2 className={sectionTitleClassName}>{t('activity.title')}</h2>
        <div className="flex flex-wrap items-center gap-2">
          {failed.length > 0 ? (
            <Button
              variant="secondary"
              size="xs"
              disabled={busy !== ''}
              onClick={() => void onReprocessFailed()}
            >
              {busy === 'reprocess' ? t('activity.queueing') : t('documents.reprocessAllFailed')}
            </Button>
          ) : null}
          <Button
            variant="secondary"
            size="xs"
            disabled={busy !== ''}
            onClick={() => void onStopAll()}
          >
            {busy === 'stop' ? t('activity.stopping') : t('activity.stopAll')}
          </Button>
          <Button
            variant="secondary"
            size="xs"
            disabled={busy !== ''}
            onClick={() => void onDiscard()}
          >
            {busy === 'discard' ? t('activity.deleting') : t('activity.deleteUnprocessed')}
          </Button>
        </div>
      </div>
      <p className="mb-4 text-sm text-ink-soft">{t('activity.intro')}</p>

      {notice ? <p className="mb-3 text-sm text-ink-soft">{notice}</p> : null}
      {actionError ? <p className="mb-3 text-sm text-madder">{actionError}</p> : null}
      {error ? <p className="mb-3 text-sm text-madder">{error}</p> : null}

      {loading && jobs.length === 0 ? (
        <p className="text-sm text-ink-soft">{t('activity.loading')}</p>
      ) : jobs.length === 0 ? (
        <p className="text-sm text-ink-soft">{t('activity.empty')}</p>
      ) : (
        <div className="flex flex-col gap-6">
          <JobGroup title={t('activity.inProgress')} jobs={active} tick={tick} queueMoving={anyRunning} onReprocessed={reload} />
          <JobGroup title={t('activity.recentlyCancelled')} jobs={cancelled} tick={tick} queueMoving={anyRunning} onReprocessed={reload} />
          <JobGroup title={t('activity.recentlyFailed')} jobs={failed} tick={tick} queueMoving={anyRunning} onReprocessed={reload} />
          {total > jobs.length ? (
            <p className="text-xs text-ink-soft">
              {t('activity.showingFirst', { shown: jobs.length, total })}
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
  queueMoving,
  onReprocessed,
}: {
  title: string
  jobs: ProcessingJobRecord[]
  tick: number
  queueMoving: boolean
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
          <JobRow key={job.id} job={job} tick={tick} queueMoving={queueMoving} onReprocessed={onReprocessed} />
        ))}
      </ul>
    </div>
  )
}

function JobRow({
  job,
  tick,
  queueMoving,
  onReprocessed,
}: {
  job: ProcessingJobRecord
  tick: number
  queueMoving: boolean
  onReprocessed: () => Promise<void>
}) {
  const [requeueing, setRequeueing] = useState(false)
  const [requeueError, setRequeueError] = useState('')
  const title =
    job.expand?.document?.title?.trim() || job.expand?.document?.file || t('common.untitledDocument')
  const elapsed = jobDurationMs(job, tick)

  async function onReprocess() {
    setRequeueing(true)
    setRequeueError('')
    try {
      await reprocessDocuments([job.document])
      await onReprocessed()
    } catch (err) {
      setRequeueError(err instanceof Error ? err.message : t('activity.reprocessError'))
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
              {requeueing ? t('activity.queueing') : t('activity.reprocess')}
            </Button>
          ) : null}
        </div>
      </div>

      <ProcessingStatus summary={summarizeJob(job, tick, queueMoving)} />
      <ProcessingSteps job={job} now={tick} collapsed />
      {requeueError ? <p className="text-xs text-madder">{requeueError}</p> : null}
    </li>
  )
}
