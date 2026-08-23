import { useEffect, useState } from 'react'
import { countFailedDocuments, reprocessFailedDocuments } from '../lib/api/documents'
import {
  getActiveJobCounts,
  pruneStaleTaxonomy,
  reindexSearch,
  scanDuplicates,
  type ActiveJobCounts,
  type DuplicateScanResult,
  type TaxonomyPruneResult,
} from '../lib/api/maintenance'
import { REPROCESS_MODE_LABELS, type ReprocessMode } from '../lib/processing'
import { Button, labelTextClassName, sectionClassName, sectionTitleClassName } from '../components/ui'

const selectClassName =
  'rounded-xs border border-line-strong bg-bright px-3 py-2 text-sm outline-none focus:border-oxblood focus:ring-1 focus:ring-oxblood'

// How often the in-flight job count is refreshed while the page is open.
const activeJobsPollMs = 5_000

// Batch sizes offered for a reprocess sweep. The worker drains serially, so a
// bigger batch does not finish sooner — it only commits more AI spend up front.
const reprocessBatchSizes = [50, 100, 500] as const
const reprocessModes: ReprocessMode[] = ['auto', 'full', 'extraction']

function countLabel(count: number, singular: string, plural: string) {
  return `${count} ${count === 1 ? singular : plural}`
}

function activeJobsTotal(counts: ActiveJobCounts | null) {
  return counts ? counts.pending + counts.running : 0
}

function activeJobsLabel(counts: ActiveJobCounts) {
  const pending = countLabel(counts.pending, 'job', 'jobs')
  const running = countLabel(counts.running, 'job', 'jobs')
  return `${pending} pending, ${running} running`
}

function pruneSummary(result: TaxonomyPruneResult) {
  const parts = [
    countLabel(result.tags, 'tag', 'tags'),
    countLabel(result.correspondents, 'correspondent', 'correspondents'),
    countLabel(result.document_types, 'document type', 'document types'),
  ]
  return `Removed ${parts.join(', ')}.`
}

// Admin access is enforced by the route's beforeLoad guard, so this page can
// assume the caller is an admin.
export function ManagementPage() {
  const [scanning, setScanning] = useState(false)
  const [scanResult, setScanResult] = useState<DuplicateScanResult | null>(null)
  const [reindexing, setReindexing] = useState(false)
  const [pruning, setPruning] = useState(false)
  const [activeJobs, setActiveJobs] = useState<ActiveJobCounts | null>(null)
  const [failedCount, setFailedCount] = useState<number | null>(null)
  const [failedCountLoaded, setFailedCountLoaded] = useState(false)
  const [reprocessing, setReprocessing] = useState(false)
  const [reprocessMode, setReprocessMode] = useState<ReprocessMode>('auto')
  const [reprocessBatch, setReprocessBatch] = useState<number>(100)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  // Pruning taxonomy while documents are still processing could delete an entity
  // a running job is about to attach, so the queue is polled to gate that button.
  useEffect(() => {
    let active = true

    async function refresh() {
      try {
        const counts = await getActiveJobCounts()
        if (active) setActiveJobs(counts)
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

  // The failed count is what the sweep acts on, so it is refreshed on load and
  // after every batch rather than polled — batches are the only thing that moves
  // it downward from this page.
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
    const confirmed = window.confirm(
      `Reprocess ${countLabel(batch, 'failed document', 'failed documents')}?\n\n` +
        `Steps: ${REPROCESS_MODE_LABELS[reprocessMode]}\n\n` +
        'Existing metadata may be overwritten.',
    )
    if (!confirmed) return

    try {
      setReprocessing(true)
      setError('')
      setSuccess('')
      const result = await reprocessFailedDocuments({
        limit: reprocessBatch,
        mode: reprocessMode,
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

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Stale data</h2>
          <p className="text-xs text-ink-soft">
            Deletes tags, correspondents and document types that no document points at any more —
            left behind by deleted documents, renames, or an aborted import. Documents are never
            touched. Blocked while documents are processing, so entities a job is about to attach
            are not swept up.
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

        {error && <p className="text-sm text-madder">{error}</p>}
        {success && <p className="text-sm text-forest">{success}</p>}
      </div>
    </div>
  )
}
