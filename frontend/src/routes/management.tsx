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
  scanIMAPRange,
  startEmbeddingBackfill,
  type ActiveJobCounts,
  type DuplicateScanResult,
  type EmbeddingBackfillState,
  type TaxonomyPruneResult,
} from '../lib/api/maintenance'
import { getLimits, type InstanceLimits } from '../lib/api/limits'
import { LimitsUsage } from '../components/LimitsUsage'
import { ResultDialog } from '../components/settings/SettingsFeedback'
import { REPROCESS_MODE_LABELS, countLabel, type ReprocessMode } from '../lib/processing'
import { Button, labelTextClassName, sectionClassName, sectionTitleClassName } from '../components/ui'

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
  const pending = countLabel(counts.pending, 'job', 'jobs')
  const running = countLabel(counts.running, 'job', 'jobs')
  return `${pending} pending, ${running} running`
}

// result.tags is not read: tags are a hand-curated vocabulary, so the prune
// leaves them alone and the count is always zero.
function pruneSummary(result: TaxonomyPruneResult) {
  const parts = [
    countLabel(result.correspondents, 'correspondent', 'correspondents'),
    countLabel(result.document_types, 'document type', 'document types'),
  ]
  return `Removed ${parts.join(' and ')}.`
}

// Admin access is enforced by the route's beforeLoad guard.
export function ManagementPage() {
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
  const [mailScanning, setMailScanning] = useState(false)
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
      `Reprocess ${countLabel(batch, 'failed document', 'failed documents')}?\n\n` +
        `Steps: ${REPROCESS_MODE_LABELS[reprocessMode]}\n` +
        (overrides ? `Models: ${overrides}\n` : '') +
        '\nExisting metadata may be overwritten.',
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
      const queued = countLabel(result.queued, 'document', 'documents')
      setSuccess(
        result.remaining > 0
          ? `Queued ${queued}. ${countLabel(result.remaining, 'document', 'documents')} still failed — run another batch once the queue drains.`
          : `Queued ${queued}. No failed documents left.`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reprocess failed')
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
      `Embed ${countLabel(missing, 'document', 'documents')}?\n\n` +
        'Their text is sent to the embedding provider, which bills for it.',
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
          ? `Embedding ${countLabel(missing, 'document', 'documents')} in the background.`
          : 'A sweep is already running; this page follows its progress.',
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Embedding backfill failed')
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
        `Scan finished: ${result.scanned} scanned, ${result.exact_marked} exact marked, ${result.near_marked} near marked.`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Duplicate scan failed')
    } finally {
      setScanning(false)
    }
  }

  async function onScanMailbox() {
    try {
      setMailScanning(true)
      setError('')
      setSuccess('')
      const result = await scanIMAPRange(mailFrom, mailTo)
      setSuccess(
        `Mailbox scan finished: ${result.created} imported, ${result.skipped} already in the library, ${result.failed} failed.`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Mailbox scan failed')
    } finally {
      setMailScanning(false)
    }
  }

  async function onReindexSearch() {
    try {
      setReindexing(true)
      setError('')
      setSuccess('')
      const result = await reindexSearch()
      setSuccess(`Reindexed ${result.indexed} documents.`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Search reindex failed')
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
          `Processing in flight (${activeJobsLabel(counts)}). Try again when the queue is idle.`,
        )
        return
      }
      const result = await pruneStaleTaxonomy()
      setSuccess(pruneSummary(result))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Stale data cleanup failed')
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
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">Management</h1>
        <p className="mt-1 text-sm text-ink-soft">
          Maintenance tasks that run over the whole library. Admin only.
        </p>
      </div>

      <div className="flex flex-col gap-5">
        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Failed processing</h2>
          <p className="text-xs text-ink-soft">
            Queues a fresh job for documents whose processing failed. Originals are never
            touched. Jobs run one at a time, so a batch drains gradually — reprocess in batches
            rather than all at once to keep OCR and AI spend under control.
          </p>
          <div className="mt-4 flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelTextClassName}>Steps</span>
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
              <span className={labelTextClassName}>Batch</span>
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
                ? 'Queueing...'
                : `Reprocess ${Math.min(reprocessBatch, failedCount ?? 0)} failed`}
            </Button>
          </div>
          <div className="mt-4">
            <JobOverrideFields value={reprocessOverrides} onChange={setReprocessOverrides} />
          </div>
          <p className="mt-3 text-xs text-ink-soft">
            {!failedCountLoaded
              ? 'Loading the failed document count...'
              : failedCount === null
                ? 'Could not read the failed document count.'
                : failedCount === 0
                  ? 'No documents have failed processing.'
                  : `${countLabel(failedCount, 'document has', 'documents have')} failed processing.`}
            {activeJobs && jobsInFlight && ` Queue: ${activeJobsLabel(activeJobs)}.`}
          </p>
        </section>

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Duplicates</h2>
          <p className="text-xs text-ink-soft">
            Backfills missing checksums and fingerprints, then marks exact duplicates (and near
            duplicates, if near-duplicate detection is enabled in Settings).
          </p>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <Button variant="secondary" disabled={scanning} onClick={() => void onScanDuplicates()}>
              {scanning ? 'Scanning...' : 'Scan for duplicates'}
            </Button>
            {scanResult && (
              <p className="text-xs text-ink-soft">
                Backfilled {scanResult.checksum_backfilled} checksums,{' '}
                {scanResult.fingerprints_filled} fingerprints.
              </p>
            )}
          </div>
        </section>

        {ingestImap && (
          <section className={sectionClassName}>
            <h2 className={sectionTitleClassName}>Mailbox</h2>
            <p className="text-xs text-ink-soft">
              The mailbox scan imports only mail received after it was set up in Settings → Ingest.
              This imports the attachments of older mail received between two days, inclusive.
              Messages are never moved or deleted, and attachments already in the library are
              skipped.
            </p>
            <div className="mt-4 flex flex-wrap items-end gap-3">
              <label className="flex flex-col gap-1">
                <span className={labelTextClassName}>Received from</span>
                <input
                  type="date"
                  className={selectClassName}
                  value={mailFrom}
                  max={mailTo || undefined}
                  onChange={(event) => setMailFrom(event.target.value)}
                />
              </label>
              <label className="flex flex-col gap-1">
                <span className={labelTextClassName}>Received to</span>
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
                disabled={mailScanning || !mailFrom || !mailTo}
                onClick={() => void onScanMailbox()}
              >
                {mailScanning ? 'Scanning...' : 'Scan mailbox'}
              </Button>
            </div>
          </section>
        )}

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Stale data</h2>
          <p className="text-xs text-ink-soft">
            Deletes correspondents and document types that no document points at any more — left
            behind by deleted documents, renames, or an aborted import. Documents are never touched,
            and neither are tags: you create those by hand, so an unused one is simply one you have
            not applied yet. Blocked while documents are processing, so entities a job is about to
            attach are not swept up.
          </p>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <Button
              variant="secondary"
              disabled={pruning || jobsInFlight}
              onClick={() => void onPruneStale()}
            >
              {pruning ? 'Clearing...' : 'Clear stale data'}
            </Button>
            {jobsInFlight && activeJobs && (
              <p className="text-xs text-amber-700">
                Waiting for the queue to drain: {activeJobsLabel(activeJobs)}.
              </p>
            )}
          </div>
        </section>

        {limits && (limits.enforced || (limits.misconfigured?.length ?? 0) > 0) && (
          <section className={sectionClassName}>
            <h2 className={sectionTitleClassName}>Instance limits</h2>
            <p className="text-xs text-ink-soft">
              Set by the operator through <code>LIMIT_*</code> environment variables and read at
              startup, so they cannot be changed from Settings. Lowering a limit under an existing
              library never deletes anything &mdash; it only refuses the next addition.
            </p>
            {limits.enforced ? (
              <LimitsUsage limits={limits} className="mt-4 border-0 bg-transparent p-0" />
            ) : (
              <p className="mt-4 text-xs text-ink-soft">No limits are in effect.</p>
            )}
            {(limits.misconfigured?.length ?? 0) > 0 && (
              <p className="mt-4 text-sm text-madder">
                Could not read {limits.misconfigured?.join(', ')}. Each fell back to unlimited, so
                these are not being enforced.
              </p>
            )}
            {limits.enforced && (
              <p className="mt-4 text-xs text-ink-faint">
                Documents added before this version was installed count as zero pages and zero
                bytes, so those two figures can read low on an upgraded library.
              </p>
            )}
          </section>
        )}

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Search index</h2>
          <p className="text-xs text-ink-soft">
            Full-text search is a derived Bleve index. Rebuild it if search results look stale after
            imports or a crash.
          </p>
          <div className="mt-4">
            <Button variant="secondary" disabled={reindexing} onClick={() => void onReindexSearch()}>
              {reindexing ? 'Reindexing...' : 'Rebuild search index'}
            </Button>
          </div>
        </section>

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Embeddings</h2>
          <p className="text-xs text-ink-soft">
            Deep Search also retrieves by meaning, which needs a vector for every passage. Uploads
            are embedded as they are processed; documents that pre-date the embedding model, that
            arrived through an import, or whose text has since changed are not. This works through
            those in the background, a batch at a time.
          </p>
          {!embeddingLoaded ? (
            <p className="mt-4 text-xs text-ink-soft">Loading the embedding backlog...</p>
          ) : embeddingStats === null ? (
            <p className="mt-4 text-xs text-ink-soft">Could not read the embedding backlog.</p>
          ) : !embeddingStats.enabled ? (
            <p className="mt-4 text-xs text-amber-700">
              No embedding model is bound, so there is nothing to embed with. Choose one in{' '}
              <Link to="/settings/ai" className="font-medium underline">
                Settings
              </Link>
              .
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
                    ? `Embedding... ${embeddingStats.embedded.toLocaleString()} of ${embeddingStats.total.toLocaleString()}`
                    : `Embed ${countLabel(embeddingMissing, 'missing document', 'missing documents')}`}
                </Button>
                {embeddingRunning && (
                  <p className="text-xs text-ink-soft">
                    Running in the background. Leaving this page does not stop it.
                  </p>
                )}
              </div>
              <p className="mt-3 text-xs text-ink-soft">
                {embeddingStats.embedded.toLocaleString()} of{' '}
                {embeddingStats.total.toLocaleString()} documents embedded with{' '}
                {embeddingStats.model}
                {embeddingStats.chunks > 0 && ` · ${embeddingStats.chunks.toLocaleString()} passages`}
                {embeddingStats.stale > 0 && ` · ${embeddingStats.stale.toLocaleString()} stale`}
                {embeddingStats.failed > 0 && ` · ${embeddingStats.failed.toLocaleString()} failed`}.
              </p>
            </>
          )}
        </section>

      </div>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </div>
  )
}
