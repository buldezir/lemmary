import { useEffect, useState } from 'react'
import { Navigate } from '@tanstack/react-router'
import {
  ensureAuth,
  isAdmin,
  reindexSearch,
  scanDuplicates,
  type DuplicateScanResult,
} from '../lib/pocketbase'

const sectionClassName = 'rounded-lg border border-stone-200 bg-stone-50 p-5'
const sectionTitleClassName = 'mb-4 text-sm font-semibold text-stone-950'
const actionButtonClassName =
  'rounded-md border border-stone-300 bg-white px-4 py-2 text-sm font-medium text-stone-950 transition-colors hover:bg-stone-100 disabled:cursor-not-allowed disabled:opacity-50'

export function ManagementPage() {
  const [allowed, setAllowed] = useState<boolean | null>(null)
  const [scanning, setScanning] = useState(false)
  const [scanResult, setScanResult] = useState<DuplicateScanResult | null>(null)
  const [reindexing, setReindexing] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  useEffect(() => {
    let active = true

    async function load() {
      try {
        await ensureAuth()
        const admin = await isAdmin()
        if (active) setAllowed(admin)
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to check permissions')
          setAllowed(false)
        }
      }
    }

    void load()
    return () => {
      active = false
    }
  }, [])

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

  if (allowed === false) {
    return <Navigate to="/" />
  }

  if (allowed === null) {
    return <p className="text-sm text-stone-500">{error || 'Loading...'}</p>
  }

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-stone-950">Management</h1>
        <p className="mt-1 text-sm text-stone-500">
          Maintenance tasks that run over the whole library. Admin only.
        </p>
      </div>

      <div className="flex flex-col gap-5">
        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Duplicates</h2>
          <p className="text-xs text-stone-500">
            Backfills missing checksums and fingerprints, then marks exact duplicates (and near
            duplicates, if near-duplicate detection is enabled in Settings).
          </p>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <button
              type="button"
              disabled={scanning}
              onClick={() => void onScanDuplicates()}
              className={actionButtonClassName}
            >
              {scanning ? 'Scanning...' : 'Scan for duplicates'}
            </button>
            {scanResult && (
              <p className="text-xs text-stone-500">
                Backfilled {scanResult.checksum_backfilled} checksums,{' '}
                {scanResult.fingerprints_filled} fingerprints.
              </p>
            )}
          </div>
        </section>

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Search index</h2>
          <p className="text-xs text-stone-500">
            Full-text search is a derived Bleve index. Rebuild it if search results look stale after
            imports or a crash.
          </p>
          <div className="mt-4">
            <button
              type="button"
              disabled={reindexing}
              onClick={() => void onReindexSearch()}
              className={actionButtonClassName}
            >
              {reindexing ? 'Reindexing...' : 'Rebuild search index'}
            </button>
          </div>
        </section>

        {error && <p className="text-sm text-red-600">{error}</p>}
        {success && <p className="text-sm text-green-700">{success}</p>}
      </div>
    </div>
  )
}
