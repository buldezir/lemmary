import { useCallback, useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useAppMeta } from '../hooks/useAppMeta'
import {
  countFailedDocuments,
  describeJobOverrides,
  reprocessFailedDocuments,
  type JobOverrides,
} from '../lib/api/documents'
import { JobOverrideFields } from '../components/BindingOverride'
import {
  getActiveJobCounts,
  getEmbeddingBackfillState,
  pruneStaleTaxonomy,
  reindexSearch,
  scanDuplicates,
  getIMAPBackfill,
  startIMAPBackfill,
  startEmbeddingBackfill,
  type ActiveJobCounts,
  type DuplicateScanResult,
  type EmbeddingBackfillState,
  type IMAPBackfillState,
  type TaxonomyPruneResult,
} from '../lib/api/maintenance'
import { getLimits, type InstanceLimits } from '../lib/api/limits'
import { LimitsUsage } from '../components/LimitsUsage'
import { ResultDialog } from '../components/settings/SettingsFeedback'
import { REPROCESS_MODE_LABELS, type ReprocessMode } from '../lib/processing'
import { Button, labelTextClassName, sectionClassName, sectionTitleClassName } from '../components/ui'
import { lang, t, tNode } from '../i18n'

const selectClassName =
  'rounded-xs border border-line-strong bg-bright px-3 py-2 text-sm outline-none focus:border-oxblood focus:ring-1 focus:ring-oxblood'

const activeJobsPollMs = 5_000

// Only while a sweep runs: the backlog is a scan of two tables, and the counts
// move in batches of a few dozen.
const embeddingPollMs = 3_000

// The worker drains serially, so a bigger batch does not finish sooner, it only
// commits more AI spend up front.
const reprocessBatchSizes = [50, 100, 500] as const
const reprocessModes: ReprocessMode[] = ['auto', 'full', 'extraction']

function activeJobsTotal(counts: ActiveJobCounts | null) {
  return counts ? counts.pending + counts.running : 0
}

function activeJobsLabel(counts: ActiveJobCounts) {
  const pending = t('maintenance.jobsPending', { count: counts.pending })
  const running = t('maintenance.jobsRunning', { count: counts.running })
  return `${pending}, ${running}`
}

// result.tags is not read: tags are a hand-curated vocabulary, so the prune
// leaves them alone and the count is always zero.
function pruneSummary(result: TaxonomyPruneResult) {
  return t('maintenance.pruned', {
    correspondents: t('maintenance.prunedCorrespondents', { count: result.correspondents }),
    types: t('maintenance.prunedTypes', { count: result.document_types }),
  })
}

// Admin access is enforced by the route's beforeLoad guard.
export function MaintenancePage() {
  const [scanning, setScanning] = useState(false)
  const [scanResult, setScanResult] = useState<DuplicateScanResult | null>(null)
  const [reindexing, setReindexing] = useState(false)
  const [pruning, setPruning] = useState(false)
  const [activeJobs, setActiveJobs] = useState<ActiveJobCounts | null>(null)
  const [limits, setLimits] = useState<InstanceLimits | null>(null)
  const [failedCount, setFailedCount] = useState<number | null>(null)
  const [failedCountLoaded, setFailedCountLoaded] = useState(false)
  const [reprocessing, setReprocessing] = useState(false)
  const [reprocessMode, setReprocessMode] = useState<ReprocessMode>('auto')
  const [reprocessBatch, setReprocessBatch] = useState<number>(100)
  const [reprocessOverrides, setReprocessOverrides] = useState<JobOverrides>({})
  const [embedding, setEmbedding] = useState<EmbeddingBackfillState | null>(null)
  const [embeddingLoaded, setEmbeddingLoaded] = useState(false)
  const [embeddingStarting, setEmbeddingStarting] = useState(false)
  const { ingestImap } = useAppMeta()
  const [mailFrom, setMailFrom] = useState('')
  const [mailTo, setMailTo] = useState('')
  const [mailStarting, setMailStarting] = useState(false)
  const [mailBackfill, setMailBackfill] = useState<IMAPBackfillState | null>(null)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const closeResult = useCallback(() => {
    setError('')
    setSuccess('')
  }, [])

  // Declared above the effect that polls on it: a sweep runs in the background
  // on the server, so this flag is what turns the poll on and off.
  const embeddingRunning = embedding?.running ?? false

  // Pruning taxonomy while documents are still processing could delete an entity
  // a running job is about to attach, so the queue is polled to gate that button.
  useEffect(() => {
    let active = true
    getLimits()
      .then((next) => {
        if (active) setLimits(next)
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [])

  useEffect(() => {
    let active = true
    let hadJobs = false

    async function refresh() {
      try {
        const counts = await getActiveJobCounts()
        if (!active) return
        setActiveJobs(counts)
        // Jobs finishing move the failed count both ways; one more read after
        // the queue drains picks up the last job's outcome.
        const hasJobs = activeJobsTotal(counts) > 0
        if (hasJobs || hadJobs) {
          const failed = await countFailedDocuments().catch(() => null)
          if (active && failed !== null) setFailedCount(failed)
        }
        hadJobs = hasJobs
      } catch {
        // An unknown count must not wedge the page: treat it as "cannot tell".
        if (active) setActiveJobs(null)
      }
    }

    void refresh()
    const timer = setInterval(() => void refresh(), activeJobsPollMs)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    let active = true
    countFailedDocuments()
      .then((count) => {
        if (active) {
          setFailedCount(count)
          setFailedCountLoaded(true)
        }
      })
      .catch(() => {
        if (active) {
          setFailedCount(null)
          setFailedCountLoaded(true)
        }
      })
    return () => {
      active = false
    }
  }, [])

  async function onReprocessFailed() {
    if (!failedCount) return

    const batch = Math.min(reprocessBatch, failedCount)
    const overrides = describeJobOverrides(reprocessOverrides)
    const confirmed = window.confirm(
      `${t('maintenance.reprocessConfirm', { count: batch })}\n\n` +
        `${t('maintenance.confirmSteps', { steps: REPROCESS_MODE_LABELS[reprocessMode] })}\n` +
        (overrides ? `${t('maintenance.confirmModels', { models: overrides })}\n` : '') +
        `\n${t('maintenance.confirmOverwrite')}`,
    )
    if (!confirmed) return

    try {
      setReprocessing(true)
      setError('')
      setSuccess('')
      const result = await reprocessFailedDocuments({
        limit: reprocessBatch,
        mode: reprocessMode,
        overrides: reprocessOverrides,
      })
      setFailedCount(result.remaining)
      const queued = t('maintenance.queued', { count: result.queued })
      setSuccess(
        result.remaining > 0
          ? `${queued} ${t('maintenance.stillFailed', { count: result.remaining })}`
          : `${queued} ${t('maintenance.noneLeft')}`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : t('maintenance.reprocessFailed'))
      // The batch may have queued part of the set before failing.
      setFailedCount(await countFailedDocuments().catch(() => null))
    } finally {
      setReprocessing(false)
    }
  }

  // Polled only while a sweep is running, for the reason above.
  useEffect(() => {
    let active = true

    async function refresh() {
      try {
        const next = await getEmbeddingBackfillState()
        if (active) setEmbedding(next)
      } catch {
        // An unknown backlog must not wedge the page: treat it as "cannot tell".
        if (active) setEmbedding(null)
      } finally {
        if (active) setEmbeddingLoaded(true)
      }
    }

    void refresh()
    if (!embeddingRunning) {
      return () => {
        active = false
      }
    }
    const timer = setInterval(() => void refresh(), embeddingPollMs)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [embeddingRunning])

  async function onEmbedMissing() {
    const missing = embedding?.stats.pending ?? 0
    if (!missing) return

    const confirmed = window.confirm(
      t('maintenance.embedConfirm', { count: missing }),
    )
    if (!confirmed) return

    try {
      setEmbeddingStarting(true)
      setError('')
      setSuccess('')
      const next = await startEmbeddingBackfill()
      setEmbedding(next)
      setEmbeddingLoaded(true)
      setSuccess(
        next.started
          ? t('maintenance.embeddingStarted', { count: missing })
          : t('maintenance.sweepRunning'),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : t('maintenance.embeddingFailed'))
    } finally {
      setEmbeddingStarting(false)
    }
  }

  async function onScanDuplicates() {
    try {
      setScanning(true)
      setError('')
      setSuccess('')
      setScanResult(null)
      const result = await scanDuplicates()
      setScanResult(result)
      setSuccess(
        t('maintenance.scanFinished', {
          scanned: result.scanned,
          exact: result.exact_marked,
          near: result.near_marked,
        }),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : t('maintenance.duplicateScanFailed'))
    } finally {
      setScanning(false)
    }
  }

  const mailRunning = mailBackfill?.running ?? false

  // Picks up a backfill still running from before a reload.
  useEffect(() => {
    if (!ingestImap) return
    let active = true
    getIMAPBackfill()
      .then((next) => {
        if (active) setMailBackfill(next)
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [ingestImap])

  useEffect(() => {
    if (!mailRunning) return
    let active = true
    const timer = setInterval(() => {
      getIMAPBackfill()
        .then((next) => {
          if (!active) return
          setMailBackfill(next)
          if (next.running) return
          const counts = t('maintenance.mailCounts', {
            created: next.created,
            skipped: next.skipped,
            failed: next.failed,
          })
          if (next.error) setError(t('maintenance.mailStopped', { error: next.error, counts }))
          else setSuccess(t('maintenance.mailFinished', { counts }))
        })
        .catch(() => {})
    }, embeddingPollMs)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [mailRunning])

  async function onScanMailbox() {
    try {
      setMailStarting(true)
      setError('')
      setSuccess('')
      setMailBackfill(await startIMAPBackfill(mailFrom, mailTo))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('maintenance.mailFailed'))
    } finally {
      setMailStarting(false)
    }
  }

  async function onReindexSearch() {
    try {
      setReindexing(true)
      setError('')
      setSuccess('')
      const result = await reindexSearch()
      setSuccess(t('maintenance.reindexed', { count: result.indexed }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('maintenance.reindexFailed'))
    } finally {
      setReindexing(false)
    }
  }

  async function onPruneStale() {
    try {
      setPruning(true)
      setError('')
      setSuccess('')
      // The polled count can be up to activeJobsPollMs stale; re-check so a job
      // started since the last poll still blocks the prune.
      const counts = await getActiveJobCounts().catch(() => null)
      setActiveJobs(counts)
      if (counts && counts.pending + counts.running > 0) {
        setError(
          t('maintenance.inFlight', { jobs: activeJobsLabel(counts) }),
        )
        return
      }
      const result = await pruneStaleTaxonomy()
      setSuccess(pruneSummary(result))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('maintenance.pruneFailed'))
    } finally {
      setPruning(false)
    }
  }

  const jobsInFlight = activeJobsTotal(activeJobs) > 0
  const embeddingStats = embedding?.stats ?? null
  const embeddingMissing = embeddingStats?.pending ?? 0

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6">
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">{t('maintenance.title')}</h1>
        <p className="mt-1 text-sm text-ink-soft">
          {t('maintenance.intro')}
        </p>
      </div>

      <div className="flex flex-col gap-5">
        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>{t('maintenance.failedTitle')}</h2>
          <p className="text-xs text-ink-soft">
            {t('maintenance.failedHint')}
          </p>
          <div className="mt-4 flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelTextClassName}>{t('maintenance.steps')}</span>
              <select
                value={reprocessMode}
                onChange={(event) => setReprocessMode(event.target.value as ReprocessMode)}
                className={selectClassName}
              >
                {reprocessModes.map((mode) => (
                  <option key={mode} value={mode}>
                    {REPROCESS_MODE_LABELS[mode]}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelTextClassName}>{t('maintenance.batch')}</span>
              <select
                value={reprocessBatch}
                onChange={(event) => setReprocessBatch(Number(event.target.value))}
                className={selectClassName}
              >
                {reprocessBatchSizes.map((size) => (
                  <option key={size} value={size}>
                    {size}
                  </option>
                ))}
              </select>
            </label>
            <Button
              variant="secondary"
              disabled={reprocessing || !failedCount}
              onClick={() => void onReprocessFailed()}
            >
              {reprocessing
                ? t('maintenance.queueing')
                : t('maintenance.reprocessButton', {
                    count: Math.min(reprocessBatch, failedCount ?? 0),
                  })}
            </Button>
          </div>
          <div className="mt-4">
            <JobOverrideFields value={reprocessOverrides} onChange={setReprocessOverrides} />
          </div>
          <p className="mt-3 text-xs text-ink-soft">
            {!failedCountLoaded
              ? t('maintenance.failedLoading')
              : failedCount === null
                ? t('maintenance.failedUnknown')
                : failedCount === 0
                  ? t('maintenance.failedNone')
                  : t('maintenance.failedSome', { count: failedCount })}
            {activeJobs &&
              jobsInFlight &&
              ` ${t('maintenance.queue', { jobs: activeJobsLabel(activeJobs) })}`}
          </p>
        </section>

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>{t('maintenance.duplicatesTitle')}</h2>
          <p className="text-xs text-ink-soft">
            {t('maintenance.duplicatesHint')}
          </p>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <Button variant="secondary" disabled={scanning} onClick={() => void onScanDuplicates()}>
              {scanning ? t('maintenance.scanning') : t('maintenance.scanDuplicates')}
            </Button>
            {scanResult && (
              <p className="text-xs text-ink-soft">
                {t('maintenance.backfilled', {
                  checksums: scanResult.checksum_backfilled,
                  fingerprints: scanResult.fingerprints_filled,
                })}
              </p>
            )}
          </div>
        </section>

        {ingestImap && (
          <section className={sectionClassName}>
            <h2 className={sectionTitleClassName}>{t('maintenance.mailboxTitle')}</h2>
            <p className="text-xs text-ink-soft">
              {t('maintenance.mailboxHint')}
            </p>
            <div className="mt-4 flex flex-wrap items-end gap-3">
              <label className="flex flex-col gap-1">
                <span className={labelTextClassName}>{t('maintenance.receivedFrom')}</span>
                <input
                  type="date"
                  className={selectClassName}
                  value={mailFrom}
                  max={mailTo || undefined}
                  onChange={(event) => setMailFrom(event.target.value)}
                />
              </label>
              <label className="flex flex-col gap-1">
                <span className={labelTextClassName}>{t('maintenance.receivedTo')}</span>
                <input
                  type="date"
                  className={selectClassName}
                  value={mailTo}
                  min={mailFrom || undefined}
                  onChange={(event) => setMailTo(event.target.value)}
                />
              </label>
              <Button
                variant="secondary"
                disabled={mailStarting || mailRunning || !mailFrom || !mailTo}
                onClick={() => void onScanMailbox()}
              >
                {mailStarting || mailRunning ? t('maintenance.scanning') : t('maintenance.scanMailbox')}
              </Button>
            </div>
            {mailBackfill?.running && (
              <p className="mt-3 text-xs text-ink-soft">
                {t('maintenance.mailRunning', { from: mailBackfill.from, to: mailBackfill.to })}
              </p>
            )}
          </section>
        )}

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>{t('maintenance.staleTitle')}</h2>
          <p className="text-xs text-ink-soft">
            {t('maintenance.staleHint')}
          </p>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <Button
              variant="secondary"
              disabled={pruning || jobsInFlight}
              onClick={() => void onPruneStale()}
            >
              {pruning ? t('maintenance.clearing') : t('maintenance.clearStale')}
            </Button>
            {jobsInFlight && activeJobs && (
              <p className="text-xs text-amber-700">
                {t('maintenance.waiting', { jobs: activeJobsLabel(activeJobs) })}
              </p>
            )}
          </div>
        </section>

        {limits && (limits.enforced || (limits.misconfigured?.length ?? 0) > 0) && (
          <section className={sectionClassName}>
            <h2 className={sectionTitleClassName}>{t('maintenance.limitsTitle')}</h2>
            <p className="text-xs text-ink-soft">
              {tNode('maintenance.limitsHint', { env: <code>LIMIT_*</code> })}
            </p>
            {limits.enforced ? (
              <LimitsUsage limits={limits} className="mt-4 border-0 bg-transparent p-0" />
            ) : (
              <p className="mt-4 text-xs text-ink-soft">{t('maintenance.noLimits')}</p>
            )}
            {(limits.misconfigured?.length ?? 0) > 0 && (
              <p className="mt-4 text-sm text-madder">
                {t('maintenance.misconfigured', { names: limits.misconfigured?.join(', ') ?? '' })}
              </p>
            )}
            {limits.enforced && (
              <p className="mt-4 text-xs text-ink-faint">
                {t('maintenance.limitsUpgrade')}
              </p>
            )}
          </section>
        )}

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>{t('maintenance.searchTitle')}</h2>
          <p className="text-xs text-ink-soft">
            {t('maintenance.searchHint')}
          </p>
          <div className="mt-4">
            <Button variant="secondary" disabled={reindexing} onClick={() => void onReindexSearch()}>
              {reindexing ? t('maintenance.reindexing') : t('maintenance.rebuild')}
            </Button>
          </div>
        </section>

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>{t('maintenance.embeddingsTitle')}</h2>
          <p className="text-xs text-ink-soft">
            {t('maintenance.embeddingsHint')}
          </p>
          {!embeddingLoaded ? (
            <p className="mt-4 text-xs text-ink-soft">{t('maintenance.embeddingLoading')}</p>
          ) : embeddingStats === null ? (
            <p className="mt-4 text-xs text-ink-soft">{t('maintenance.embeddingUnknown')}</p>
          ) : !embeddingStats.enabled ? (
            <p className="mt-4 text-xs text-amber-700">
              {tNode('maintenance.noEmbeddingModel', {
                link: (
                  <Link to="/settings/ai" className="font-medium underline">
                    {t('maintenance.settingsLink')}
                  </Link>
                ),
              })}
            </p>
          ) : (
            <>
              <div className="mt-4 flex flex-wrap items-center gap-3">
                <Button
                  variant="secondary"
                  disabled={embeddingStarting || embeddingRunning || embeddingMissing === 0}
                  onClick={() => void onEmbedMissing()}
                >
                  {embeddingRunning
                    ? t('maintenance.embeddingProgress', {
                        embedded: embeddingStats.embedded.toLocaleString(lang),
                        total: embeddingStats.total.toLocaleString(lang),
                      })
                    : t('maintenance.embedMissing', { count: embeddingMissing })}
                </Button>
                {embeddingRunning && (
                  <p className="text-xs text-ink-soft">
                    {t('maintenance.embeddingBackground')}
                  </p>
                )}
              </div>
              <p className="mt-3 text-xs text-ink-soft">
                {t('maintenance.embeddedWith', {
                  embedded: embeddingStats.embedded.toLocaleString(lang),
                  total: embeddingStats.total.toLocaleString(lang),
                  model: embeddingStats.model,
                })}
                {embeddingStats.chunks > 0 &&
                  ` · ${t('maintenance.statsPassages', {
                    count: embeddingStats.chunks,
                    n: embeddingStats.chunks.toLocaleString(lang),
                  })}`}
                {embeddingStats.stale > 0 &&
                  ` · ${t('maintenance.statsStale', { n: embeddingStats.stale.toLocaleString(lang) })}`}
                {embeddingStats.failed > 0 &&
                  ` · ${t('maintenance.statsFailed', { n: embeddingStats.failed.toLocaleString(lang) })}`}
                .
              </p>
            </>
          )}
        </section>

      </div>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </div>
  )
}
