import { Link } from '@tanstack/react-router'
import { fetchDocumentTimeline } from '../lib/api/documents'
import { UNDATED_PERIOD, activePeriod, periodRange } from '../lib/timeline'
import { hasActiveFilters } from '../lib/documentQuery'
import { useAsync } from '../hooks/useAsync'
import { useDocumentFilterOptions, useDocumentList } from '../hooks/useDocumentList'
import { useStoredFlag } from '../hooks/useStoredFlag'
import type { BulkMode } from '../components/DocumentBulkBar'
import { DocumentFilters } from '../components/DocumentFilters'
import { DocumentGrid } from '../components/DocumentGrid'
import { DocumentTimeline } from '../components/DocumentTimeline'

/** The whole library, filtered by hand. The Inbox is its own page. */
export function IndexPage() {
  const list = useDocumentList({ route: '/' })
  const filterOptions = useDocumentFilterOptions()
  const { query, statusFilter, updateQuery, documents, loading, error } = list

  // A view preference, not a filter, so it lives in localStorage rather than the
  // URL: a shared link describes the list, not how the reader arranged their own
  // screen, but their own arrangement should survive a reload.
  const [showTimeline, setShowTimeline] = useStoredFlag('lemmary.showTimeline', true)
  // The timeline counts the whole library rather than the current query, so it
  // only goes stale when the library itself changes.
  const timeline = useAsync(fetchDocumentTimeline, [list.libraryVersion])

  // What the current filter makes possible: a list of failures can be requeued,
  // a list of doubtful documents can be cleared.
  const bulkMode: BulkMode | null =
    statusFilter === 'failed' ? 'reprocess' : statusFilter === 'needs_review' ? 'review' : null

  // The timeline has no date filter of its own: picking a period writes the
  // From/To inputs, and the highlight is read back out of them. "No date" is
  // the exception -- no range can express it -- so it writes its own flag, and
  // the two clear each other because a document cannot be both.
  function onSelectPeriod(period: string | null) {
    const range = periodRange(period)
    updateQuery({ from: range.from, to: range.to, undated: period === UNDATED_PERIOD })
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">Documents</h2>
          <p className="text-sm text-ink-soft">Upload, search, and review AI-extracted metadata.</p>
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
          active={query.undated ? UNDATED_PERIOD : activePeriod(query.from, query.to)}
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
          <DocumentFilters
            query={query}
            search={list.search}
            onSearchChange={list.setSearch}
            updateQuery={updateQuery}
            documentTypes={filterOptions.documentTypes}
            correspondents={filterOptions.correspondents}
            status={statusFilter}
          />

          {loading && <p className="text-sm text-ink-soft">Loading documents...</p>}
          {(error || filterOptions.error || timeline.error) && (
            <p className="text-sm text-madder">
              {error || filterOptions.error || timeline.error}
            </p>
          )}

          {!loading && documents.length === 0 && (
            <div className="rounded-none border border-line bg-surface py-10 text-center">
              {hasActiveFilters(query) ? (
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

          {list.message && <p className="text-sm text-forest">{list.message}</p>}

          {!loading && documents.length > 0 && <DocumentGrid list={list} bulkMode={bulkMode} />}
        </div>
      </div>
    </section>
  )
}
