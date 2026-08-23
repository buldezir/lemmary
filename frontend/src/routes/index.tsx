import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ClientResponseError } from 'pocketbase'
import { pb } from '../lib/pb'
import { ensureAuth } from '../lib/auth'
import {
  buildDocumentFilter,
  reprocessDocuments,
  searchDocuments,
  type CorrespondentRecord,
  type DocumentRecord,
  type DocumentTypeRecord,
} from '../lib/api/documents'
import { REPROCESS_MODE_LABELS, type ReprocessMode } from '../lib/processing'
import { useAsync } from '../hooks/useAsync'
import { DocumentCard } from '../components/DocumentCard'
import { FilterCombobox } from '../components/FilterCombobox'
import { Pagination } from '../components/Pagination'
import { Button } from '../components/ui'

const PAGE_SIZE = 12

const selectClassName =
  'rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900'

const reprocessModes: ReprocessMode[] = ['auto', 'full', 'extraction']

export function IndexPage() {
  const [documents, setDocuments] = useState<DocumentRecord[]>([])
  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')
  const [documentTypeFilter, setDocumentTypeFilter] = useState('all')
  const [correspondentFilter, setCorrespondentFilter] = useState('all')
  const [page, setPage] = useState(1)
  const [totalItems, setTotalItems] = useState(0)
  const [totalPages, setTotalPages] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [reprocessMode, setReprocessMode] = useState<ReprocessMode>('auto')
  const [reprocessing, setReprocessing] = useState(false)
  const [message, setMessage] = useState('')

  const filterOptions = useAsync(async () => {
    await ensureAuth()
    const [types, correspondents] = await Promise.all([
      pb.collection('document_types').getFullList<DocumentTypeRecord>({ sort: 'name' }),
      pb.collection('correspondents').getFullList<CorrespondentRecord>({ sort: 'name' }),
    ])
    return { types, correspondents }
  }, [])
  const documentTypes = filterOptions.data?.types ?? []
  const correspondents = filterOptions.data?.correspondents ?? []

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedSearch(search)
      setPage(1)
    }, 300)
    return () => window.clearTimeout(timer)
  }, [search])

  useEffect(() => {
    let active = true

    async function load(isInitial = false) {
      try {
        // Only the initial load blanks the page; realtime refreshes swap the
        // data in place instead of flashing "Loading documents...".
        if (isInitial) {
          setLoading(true)
        }
        await ensureAuth()
        const query = debouncedSearch.trim()
        const filter = buildDocumentFilter({
          status: statusFilter,
          dateFrom,
          dateTo,
          documentType: documentTypeFilter,
          correspondent: correspondentFilter,
        })
        const result = query
          ? await searchDocuments({
              q: query,
              page,
              perPage: PAGE_SIZE,
              status: statusFilter,
              documentType: documentTypeFilter,
              correspondent: correspondentFilter,
              dateFrom,
              dateTo,
            })
          : await pb.collection('documents').getList<DocumentRecord>(page, PAGE_SIZE, {
              sort: '-created',
              expand: 'tags,document_type,correspondent,duplicate_of',
              ...(filter ? { filter } : {}),
            })
        if (active) {
          setDocuments(result.items)
          setTotalItems(result.totalItems)
          setTotalPages(result.totalPages)
          setError('')
        }
      } catch (err) {
        // Overlapping refreshes (filter change + realtime) can autocancel each
        // other; the surviving request has the fresh data.
        if (err instanceof ClientResponseError && err.isAbort) {
          return
        }
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load documents')
        }
      } finally {
        if (active && isInitial) {
          setLoading(false)
        }
      }
    }

    void load(true)

    let unsubscribe: (() => void) | undefined
    void pb
      .collection('documents')
      .subscribe('*', () => {
        void load()
      })
      .then((fn) => {
        unsubscribe = fn
      })
      .catch(() => {
        // Realtime is optional; the list is still correct as of the load.
      })

    return () => {
      active = false
      unsubscribe?.()
    }
  }, [page, statusFilter, dateFrom, dateTo, documentTypeFilter, correspondentFilter, debouncedSearch])

  // Bulk reprocess exists to clear a backlog of failures, so selection is only
  // offered where that backlog is on screen.
  const selectable = statusFilter === 'failed'
  // Every action goes through selectedOnPage, never selectedIds, so ids left over
  // from another page or an earlier filter can neither be counted nor submitted.
  // That is what makes a stale selection harmless without resetting state on
  // every filter change.
  const selectedOnPage = documents.filter((document) => selectedIds.has(document.id))

  function toggleSelected(id: string) {
    setSelectedIds((current) => {
      const next = new Set(current)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  async function onReprocessSelected() {
    const ids = selectedOnPage.map((document) => document.id)
    if (ids.length === 0) return

    const confirmed = window.confirm(
      `Reprocess ${ids.length === 1 ? 'this document' : `these ${ids.length} documents`}?\n\n` +
        `Steps: ${REPROCESS_MODE_LABELS[reprocessMode]}\n\n` +
        'Existing metadata may be overwritten.',
    )
    if (!confirmed) return

    try {
      setReprocessing(true)
      setError('')
      setMessage('')
      const result = await reprocessDocuments(ids, reprocessMode)
      setSelectedIds(new Set())
      setMessage(
        result.skipped > 0
          ? `Queued ${result.queued}, skipped ${result.skipped} already in the queue.`
          : `Queued ${result.queued} for reprocessing.`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reprocess failed')
    } finally {
      setReprocessing(false)
    }
  }

  const hasActiveFilters =
    statusFilter !== 'all' ||
    dateFrom !== '' ||
    dateTo !== '' ||
    documentTypeFilter !== 'all' ||
    correspondentFilter !== 'all' ||
    debouncedSearch !== ''

  return (
    <section className="flex flex-col gap-6">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h2 className="text-xl font-semibold text-stone-950">Documents</h2>
          <p className="text-sm text-stone-500">Upload, search, and review AI-extracted metadata.</p>
        </div>
        <Link
          to="/upload"
          className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700"
        >
          Upload document
        </Link>
      </div>

      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-3 sm:flex-row">
          <input
            type="search"
            placeholder="Search title, tags, purpose, summary..."
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none placeholder:text-stone-400 focus:border-gray-900 focus:ring-1 focus:ring-gray-900"
          />
          <select
            value={statusFilter}
            onChange={(event) => {
              setStatusFilter(event.target.value)
              setPage(1)
            }}
            aria-label="Processing status"
            className={`${selectClassName} sm:w-48`}
          >
            <option value="all">All statuses</option>
            <option value="pending">Pending</option>
            <option value="processing">Processing</option>
            <option value="completed">Completed</option>
            <option value="needs_review">Needs review</option>
            <option value="failed">Failed</option>
          </select>
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-stone-500">From date</span>
            <input
              type="date"
              value={dateFrom}
              onChange={(event) => {
                setDateFrom(event.target.value)
                setPage(1)
              }}
              className={selectClassName}
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-stone-500">To date</span>
            <input
              type="date"
              value={dateTo}
              onChange={(event) => {
                setDateTo(event.target.value)
                setPage(1)
              }}
              className={selectClassName}
            />
          </label>
          <FilterCombobox
            label="Document type"
            value={documentTypeFilter}
            allLabel="All types"
            options={documentTypes.map((type) => ({ value: type.id, label: type.name }))}
            onChange={(next) => {
              setDocumentTypeFilter(next)
              setPage(1)
            }}
          />
          <FilterCombobox
            label="Correspondent"
            value={correspondentFilter}
            allLabel="All correspondents"
            options={correspondents.map((correspondent) => ({
              value: correspondent.id,
              label: correspondent.name,
            }))}
            onChange={(next) => {
              setCorrespondentFilter(next)
              setPage(1)
            }}
          />
        </div>
      </div>

      {loading && <p className="text-sm text-stone-500">Loading documents...</p>}
      {(error || filterOptions.error) && (
        <p className="text-sm text-red-600">{error || filterOptions.error}</p>
      )}

      {!loading && documents.length === 0 && (
        <div className="rounded-lg border border-dashed border-stone-300 bg-stone-50 py-12 text-center">
          {hasActiveFilters ? (
            <p className="text-sm text-stone-500">No documents match your filters.</p>
          ) : (
            <>
              <p className="text-sm text-stone-500">No documents yet.</p>
              <Link to="/upload" className="mt-1 inline-block text-sm font-medium text-gray-900 underline">
                Upload your first document
              </Link>
            </>
          )}
        </div>
      )}

      {message && <p className="text-sm text-green-700">{message}</p>}

      {!loading && documents.length > 0 && (
        <>
          {selectable && (
            <div className="flex flex-wrap items-center gap-3 rounded-lg border border-stone-200 bg-stone-50 px-4 py-3">
              <span className="text-sm text-stone-600">
                {selectedOnPage.length === 0
                  ? 'Select failed documents to reprocess.'
                  : `${selectedOnPage.length} selected`}
              </span>
              <select
                value={reprocessMode}
                onChange={(event) => setReprocessMode(event.target.value as ReprocessMode)}
                aria-label="Reprocess steps"
                className={selectClassName}
              >
                {reprocessModes.map((mode) => (
                  <option key={mode} value={mode}>
                    {REPROCESS_MODE_LABELS[mode]}
                  </option>
                ))}
              </select>
              <Button
                disabled={reprocessing || selectedOnPage.length === 0}
                onClick={() => void onReprocessSelected()}
              >
                {reprocessing ? 'Queueing...' : 'Reprocess'}
              </Button>
              <Button
                variant="secondary"
                onClick={() => setSelectedIds(new Set(documents.map((document) => document.id)))}
              >
                Select all on page
              </Button>
              {selectedOnPage.length > 0 && (
                <Button variant="secondary" onClick={() => setSelectedIds(new Set())}>
                  Clear
                </Button>
              )}
            </div>
          )}

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {documents.map((document) => (
              <DocumentCard
                key={document.id}
                document={document}
                selectable={selectable}
                selected={selectedIds.has(document.id)}
                onToggleSelect={toggleSelected}
              />
            ))}
          </div>

          <Pagination
            page={page}
            totalPages={totalPages}
            totalItems={totalItems}
            pageSize={PAGE_SIZE}
            onPageChange={setPage}
          />
        </>
      )}
    </section>
  )
}
