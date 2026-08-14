import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ensureAuth, pb } from '../lib/pocketbase'
import type { CorrespondentRecord, DocumentRecord, DocumentTypeRecord } from '../lib/pocketbase'
import { DocumentCard } from '../components/DocumentCard'
import { FilterCombobox } from '../components/FilterCombobox'
import { Pagination } from '../components/Pagination'

const PAGE_SIZE = 12

const selectClassName =
  'rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900'

function escapeFilterValue(value: string) {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
}

function buildDocumentFilter(filters: {
  status: string
  dateFrom: string
  dateTo: string
  documentType: string
  correspondent: string
  search: string
}) {
  const parts: string[] = []

  if (filters.status !== 'all') {
    parts.push(`processing_status = "${filters.status}"`)
  }
  if (filters.documentType !== 'all') {
    parts.push(`document_type = "${filters.documentType}"`)
  }
  if (filters.correspondent !== 'all') {
    parts.push(`correspondent = "${filters.correspondent}"`)
  }
  if (filters.dateFrom) {
    parts.push(`document_date >= "${filters.dateFrom}"`)
  }
  if (filters.dateTo) {
    parts.push(`document_date <= "${filters.dateTo}"`)
  }
  if (filters.search) {
    const query = escapeFilterValue(filters.search)
    parts.push(
      `(title ~ "${query}" || purpose ~ "${query}" || summary ~ "${query}" || ocr_text ~ "${query}" || tags.name ~ "${query}")`,
    )
  }

  return parts.length > 0 ? parts.join(' && ') : undefined
}

export function IndexPage() {
  const [documents, setDocuments] = useState<DocumentRecord[]>([])
  const [documentTypes, setDocumentTypes] = useState<DocumentTypeRecord[]>([])
  const [correspondents, setCorrespondents] = useState<CorrespondentRecord[]>([])
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

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedSearch(search)
      setPage(1)
    }, 300)
    return () => window.clearTimeout(timer)
  }, [search])

  useEffect(() => {
    let active = true

    async function loadFilterOptions() {
      try {
        await ensureAuth()
        const [types, cors] = await Promise.all([
          pb.collection('document_types').getFullList<DocumentTypeRecord>({ sort: 'name' }),
          pb.collection('correspondents').getFullList<CorrespondentRecord>({ sort: 'name' }),
        ])
        if (active) {
          setDocumentTypes(types)
          setCorrespondents(cors)
        }
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load filter options')
        }
      }
    }

    void loadFilterOptions()

    return () => {
      active = false
    }
  }, [])

  useEffect(() => {
    let active = true

    async function load() {
      try {
        setLoading(true)
        await ensureAuth()
        const filter = buildDocumentFilter({
          status: statusFilter,
          dateFrom,
          dateTo,
          documentType: documentTypeFilter,
          correspondent: correspondentFilter,
          search: debouncedSearch.trim(),
        })
        const result = await pb.collection('documents').getList<DocumentRecord>(page, PAGE_SIZE, {
          sort: '-created',
          expand: 'tags,document_type,correspondent',
          ...(filter ? { filter } : {}),
        })
        if (active) {
          setDocuments(result.items)
          setTotalItems(result.totalItems)
          setTotalPages(result.totalPages)
          setError('')
        }
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load documents')
        }
      } finally {
        if (active) {
          setLoading(false)
        }
      }
    }

    load()

    let unsubscribe: (() => void) | undefined

    void pb.collection('documents').subscribe('*', () => {
      load()
    }).then((fn) => {
      unsubscribe = fn
    })

    return () => {
      active = false
      unsubscribe?.()
    }
  }, [page, statusFilter, dateFrom, dateTo, documentTypeFilter, correspondentFilter, debouncedSearch])

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
      {error && <p className="text-sm text-red-600">{error}</p>}

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

      {!loading && documents.length > 0 && (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {documents.map((document) => (
              <DocumentCard key={document.id} document={document} />
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
