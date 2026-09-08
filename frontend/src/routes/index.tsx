import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { ClientResponseError } from 'pocketbase'
import { pb } from '../lib/pb'
import { ensureAuth } from '../lib/auth'
import {
  buildDocumentFilter,
  fetchDocumentTimeline,
  markDocumentsReviewed,
  reprocessDocuments,
  searchDocuments,
  type CorrespondentRecord,
  type DocumentRecord,
  type DocumentTypeRecord,
} from '../lib/api/documents'
import { activePeriod, periodRange } from '../lib/timeline'
import {
  documentQuerySearch,
  hasActiveFilters,
  parseDocumentQuery,
  type DocumentQuery,
} from '../lib/documentQuery'
import { onDocumentsChanged } from '../lib/documentEvents'
import { DOCUMENT_STATUSES, DOCUMENT_STATUS_LABELS } from '../lib/documentStatus'
import { REPROCESS_MODE_LABELS, type ReprocessMode } from '../lib/processing'
import { useAsync } from '../hooks/useAsync'
import { useStoredFlag } from '../hooks/useStoredFlag'
import { DocumentBulkBar, type BulkMode } from '../components/DocumentBulkBar'
import { DocumentCard } from '../components/DocumentCard'
import { DocumentTimeline } from '../components/DocumentTimeline'
import { FilterCombobox } from '../components/FilterCombobox'
import { Pagination } from '../components/Pagination'
import { selectClassName } from '../components/ui'

const PAGE_SIZE = 12

export function IndexPage() {
  return <DocumentListPage route="/" />
}

/** The same list, its status fixed by the path rather than by the dropdown. */
export function InboxPage() {
  return <DocumentListPage route="/inbox" />
}

function DocumentListPage({ route }: { route: '/' | '/inbox' }) {
  // The filters are the URL, not state: reloading, bookmarking or sharing the
  // page reproduces the list, and Back steps through the filters that made it.
  // Validated but sparse: the URL only carries the filters that are set, so the
  // defaults are filled back in here.
  const query = parseDocumentQuery(useSearch({ strict: false }))
  const navigate = useNavigate({ from: route })
  const inbox = route === '/inbox'
  const {
    q: debouncedSearch,
    from: dateFrom,
    to: dateTo,
    type: documentTypeFilter,
    correspondent: correspondentFilter,
    page,
  } = query
  const statusFilter = inbox ? 'needs_review' : query.status

  const [documents, setDocuments] = useState<DocumentRecord[]>([])
  // The only filter with a copy outside the URL, because it is typed one letter
  // at a time and the URL only gets the settled value.
  const [search, setSearch] = useState(debouncedSearch)
  const [syncedSearch, setSyncedSearch] = useState(debouncedSearch)
  const [totalItems, setTotalItems] = useState(0)
  const [totalPages, setTotalPages] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [reprocessMode, setReprocessMode] = useState<ReprocessMode>('auto')
  const [reprocessing, setReprocessing] = useState(false)
  const [markingReviewed, setMarkingReviewed] = useState(false)
  const [message, setMessage] = useState('')
  // Bumped whenever the library changes, to re-count the timeline.
  const [timelineVersion, setTimelineVersion] = useState(0)
  // A view preference, not a filter, so it lives in localStorage rather than the
  // URL: a shared link describes the list, not how the reader arranged their own
  // screen, but their own arrangement should survive a reload.
  const [showTimeline, setShowTimeline] = useStoredFlag('lemmary.showTimeline', true)

  // URL -> box, for Back/Forward and for a link opened with a term already in
  // it. Adjusted during render rather than in an effect, so the box never paints
  // one frame of the old term. syncedSearch is what makes this fire on a URL
  // change only: a keystroke leaves it alone, so the typing is not overwritten
  // before the debounce has had a chance to publish it.
  if (syncedSearch !== debouncedSearch) {
    setSyncedSearch(debouncedSearch)
    setSearch(debouncedSearch)
  }

  const filterOptions = useAsync(async () => {
    await ensureAuth()
    const [types, correspondents] = await Promise.all([
      pb.collection('document_types').getFullList<DocumentTypeRecord>({ sort: 'name' }),
      pb.collection('correspondents').getFullList<CorrespondentRecord>({ sort: 'name' }),
    ])
    return { types, correspondents }
  }, [])
  const timeline = useAsync(fetchDocumentTimeline, [timelineVersion])

  const documentTypes = filterOptions.data?.types ?? []
  const correspondents = filterOptions.data?.correspondents ?? []

  /**
   * Writes filters to the URL, which is what re-renders the list.
   *
   * A different filter is a different list, so it starts at page one unless the
   * patch says otherwise. Discrete controls push a history entry — Back undoes
   * the filter — while the search box replaces, so Back skips the whole phrase
   * rather than walking back one keystroke at a time.
   */
  const updateQuery = useCallback(
    (patch: Partial<DocumentQuery>, replace = false) => {
      void navigate({
        to: route,
        search: (current) => {
          const next = documentQuerySearch({
            ...parseDocumentQuery(current),
            page: 1,
            ...patch,
          })
          if (inbox) delete next.status
          return next
        },
        replace,
      })
    },
    [navigate, route, inbox],
  )

  // Box -> URL, once the typing settles. Skipped while the two already agree,
  // so the sync above cannot bounce back as a navigation.
  useEffect(() => {
    if (search === debouncedSearch) return
    const timer = window.setTimeout(() => updateQuery({ q: search }, true), 300)
    return () => window.clearTimeout(timer)
  }, [search, debouncedSearch, updateQuery])

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
        if (!active) return

        // Clearing a whole page leaves the URL past the end, where an empty
        // page reads as an empty *list*: the pager hides itself at one page, so
        // the Inbox would claim nothing was waiting while the badge counted
        // twelve. Walk back instead of reporting it.
        if (result.items.length === 0 && page > result.totalPages && result.totalItems > 0) {
          updateQuery({ page: result.totalPages }, true)
          return
        }

        setDocuments(result.items)
        setTotalItems(result.totalItems)
        setTotalPages(result.totalPages)
        setError('')
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

    function refresh() {
      void load()
      // The timeline counts the whole library rather than the current query,
      // so it only goes stale when the library itself changes.
      setTimelineVersion((version) => version + 1)
    }

    // Our own writes, which is the only signal that always arrives: realtime is
    // optional, and without this a marked-reviewed card would sit on an Inbox
    // it no longer belongs to while the header's count already said it had gone.
    const offLocal = onDocumentsChanged(refresh)

    let unsubscribe: (() => void) | undefined
    void pb
      .collection('documents')
      .subscribe('*', refresh)
      .then((fn) => {
        unsubscribe = fn
      })
      .catch(() => {
        // Realtime is optional; the list is still correct as of the load.
      })

    return () => {
      active = false
      offLocal()
      unsubscribe?.()
    }
  }, [
    page,
    statusFilter,
    dateFrom,
    dateTo,
    documentTypeFilter,
    correspondentFilter,
    debouncedSearch,
    updateQuery,
  ])

  // Keyed off the status rather than the route, so /?status=needs_review
  // behaves the same as /inbox.
  const bulkMode: BulkMode | null =
    statusFilter === 'failed' ? 'reprocess' : statusFilter === 'needs_review' ? 'review' : null
  const selectable = bulkMode !== null
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

  /**
   * Clears documents out of the Inbox. No confirmation, unlike reprocess: this
   * overwrites nothing.
   *
   * Filtered to documents still reading needs_review, on top of the
   * selectedOnPage guard, because a refresh can land between the tick and the
   * click.
   */
  async function onMarkReviewed(documentIds: string[]) {
    const ids = documents
      .filter(
        (document) =>
          documentIds.includes(document.id) && document.processing_status === 'needs_review',
      )
      .map((document) => document.id)
    if (ids.length === 0) return

    try {
      setMarkingReviewed(true)
      setError('')
      setMessage('')
      await markDocumentsReviewed(ids)
      setSelectedIds(new Set())
      setMessage(ids.length === 1 ? 'Marked reviewed.' : `Marked ${ids.length} reviewed.`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not mark as reviewed')
    } finally {
      setMarkingReviewed(false)
    }
  }

  // The timeline has no date filter of its own: picking a period writes the
  // From/To inputs, and the highlight is read back out of them.
  function onSelectPeriod(period: string | null) {
    const range = periodRange(period)
    updateQuery({ from: range.from, to: range.to })
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">
            {inbox ? 'Inbox' : 'Documents'}
          </h2>
          <p className="text-sm text-ink-soft">
            {inbox
              ? 'Documents waiting for you to check what the AI extracted.'
              : 'Upload, search, and review AI-extracted metadata.'}
          </p>
        </div>
        <Link
          to="/upload"
          className="rounded-xs bg-ink px-4 py-2 text-sm font-medium text-paper transition-colors hover:bg-oxblood"
        >
          Upload document
        </Link>
      </div>

      <div className="flex flex-col gap-4 lg:flex-row lg:items-start">
        <DocumentTimeline
          timeline={timeline.data}
          active={activePeriod(dateFrom, dateTo)}
          onSelect={onSelectPeriod}
          expanded={showTimeline}
          onToggleExpanded={() => setShowTimeline((shown) => !shown)}
          className={
            showTimeline
              ? 'order-last lg:order-first lg:sticky lg:top-6 lg:max-h-[calc(100vh-3rem)] lg:w-44 lg:shrink-0 lg:overflow-y-auto'
              : // Collapsed it is a rule down the side of the grid, so it wants the
                // row's height rather than a sticky box of its own.
                'order-last h-4 lg:order-first lg:h-auto lg:w-4 lg:shrink-0 lg:self-stretch'
          }
        />

        {/* min-w-0 so the card grid can shrink instead of pushing the sidebar. */}
        <div className="flex min-w-0 flex-1 flex-col gap-4">
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-3 sm:flex-row">
              <input
                type="search"
                placeholder="Search title, tags, purpose, summary..."
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                className="w-full rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood"
              />
              {!inbox && (
                <select
                  value={statusFilter}
                  onChange={(event) => updateQuery({ status: event.target.value })}
                  aria-label="Processing status"
                  className={`${selectClassName} sm:w-48`}
                >
                  <option value="all">All statuses</option>
                  {DOCUMENT_STATUSES.map((status) => (
                    <option key={status} value={status}>
                      {DOCUMENT_STATUS_LABELS[status]}
                    </option>
                  ))}
                </select>
              )}
            </div>

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <label className="flex flex-col gap-1">
                <span className="text-xs font-medium text-ink-soft">From date</span>
                <input
                  type="date"
                  value={dateFrom}
                  // replace: a date field fires a change per digit typed into
                  // the year, and none of those belong in history.
                  onChange={(event) => updateQuery({ from: event.target.value }, true)}
                  className={selectClassName}
                />
              </label>
              <label className="flex flex-col gap-1">
                <span className="text-xs font-medium text-ink-soft">To date</span>
                <input
                  type="date"
                  value={dateTo}
                  onChange={(event) => updateQuery({ to: event.target.value }, true)}
                  className={selectClassName}
                />
              </label>
              <FilterCombobox
                label="Document type"
                value={documentTypeFilter}
                allLabel="All types"
                options={documentTypes.map((type) => ({ value: type.id, label: type.name }))}
                onChange={(next) => updateQuery({ type: next })}
              />
              <FilterCombobox
                label="Correspondent"
                value={correspondentFilter}
                allLabel="All correspondents"
                options={correspondents.map((correspondent) => ({
                  value: correspondent.id,
                  label: correspondent.name,
                }))}
                onChange={(next) => updateQuery({ correspondent: next })}
              />
            </div>
          </div>

          {loading && <p className="text-sm text-ink-soft">Loading documents...</p>}
          {(error || filterOptions.error || timeline.error) && (
            <p className="text-sm text-madder">{error || filterOptions.error || timeline.error}</p>
          )}

          {!loading && documents.length === 0 && (
            <div className="rounded-none border border-dashed border-line-strong bg-surface py-10 text-center">
              {/* Neither of these lists is empty in the sense of "upload
                  something": hasActiveFilters is false on a bare /inbox, and
                  false on a `/` that defaults to Completed. */}
              {inbox && !hasActiveFilters(query) ? (
                <p className="text-sm text-ink-soft">Nothing waiting for review.</p>
              ) : !hasActiveFilters(query) && statusFilter === 'completed' ? (
                <>
                  <p className="text-sm text-ink-soft">No documents reviewed yet.</p>
                  <Link to="/inbox" className="mt-1 inline-block text-sm font-medium text-oxblood underline">
                    Open the Inbox
                  </Link>
                </>
              ) : hasActiveFilters(query) ? (
                <p className="text-sm text-ink-soft">No documents match your filters.</p>
              ) : (
                <>
                  <p className="text-sm text-ink-soft">No documents yet.</p>
                  <Link to="/upload" className="mt-1 inline-block text-sm font-medium text-oxblood underline">
                    Upload your first document
                  </Link>
                </>
              )}
            </div>
          )}

          {message && <p className="text-sm text-forest">{message}</p>}

          {!loading && documents.length > 0 && (
            <>
              {bulkMode && (
                <DocumentBulkBar
                  mode={bulkMode}
                  selectedCount={selectedOnPage.length}
                  busy={reprocessing || markingReviewed}
                  reprocessMode={reprocessMode}
                  onReprocessModeChange={setReprocessMode}
                  onReprocess={() => void onReprocessSelected()}
                  onMarkReviewed={() =>
                    void onMarkReviewed(selectedOnPage.map((document) => document.id))
                  }
                  onSelectAll={() =>
                    setSelectedIds(new Set(documents.map((document) => document.id)))
                  }
                  onClear={() => setSelectedIds(new Set())}
                />
              )}

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {documents.map((document) => (
                  <DocumentCard
                    key={document.id}
                    document={document}
                    selectable={selectable}
                    selected={selectedIds.has(document.id)}
                    onToggleSelect={toggleSelected}
                    onMarkReviewed={(id) => void onMarkReviewed([id])}
                    markingReviewed={markingReviewed}
                  />
                ))}
              </div>

              <Pagination
                page={page}
                totalPages={totalPages}
                totalItems={totalItems}
                pageSize={PAGE_SIZE}
                onPageChange={(next) => updateQuery({ page: next })}
              />
            </>
          )}
        </div>
      </div>
    </section>
  )
}
