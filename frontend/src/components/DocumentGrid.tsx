import { DOCUMENT_PAGE_SIZE, type DocumentList } from '../hooks/useDocumentList'
import { DocumentBulkBar, type BulkMode } from './DocumentBulkBar'
import { DocumentCard } from './DocumentCard'
import { Pagination } from './Pagination'

/**
 * A page of documents: the bulk bar for whatever this list can do to them, the
 * cards, and the pager. Identical wherever documents are listed -- what differs
 * between the lists is the heading, the filters and what an empty one means.
 */
export function DocumentGrid({
  list,
  bulkMode,
  allowDelete = false,
}: {
  list: DocumentList
  bulkMode: BulkMode | null
  /** Adds "Delete selected" to the bulk bar. */
  allowDelete?: boolean
}) {
  const {
    documents,
    jobs,
    selectedIds,
    selectedOnPage,
    toggleSelected,
    selectAll,
    clearSelection,
    reprocessMode,
    setReprocessMode,
    reprocessing,
    markingReviewed,
    deleting,
    onReprocessSelected,
    onMarkReviewed,
    onDeleteSelected,
    page,
    totalPages,
    totalItems,
    updateQuery,
  } = list

  return (
    <>
      {bulkMode && (
        <DocumentBulkBar
          mode={bulkMode}
          selectedCount={selectedOnPage.length}
          busy={reprocessing || markingReviewed || deleting}
          reprocessMode={reprocessMode}
          onReprocessModeChange={setReprocessMode}
          onReprocess={() => void onReprocessSelected()}
          onMarkReviewed={() => void onMarkReviewed(selectedOnPage.map((document) => document.id))}
          onSelectAll={selectAll}
          onClear={clearSelection}
          onDelete={allowDelete ? () => void onDeleteSelected() : undefined}
        />
      )}

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {documents.map((document) => (
          <DocumentCard
            key={document.id}
            document={document}
            job={jobs.get(document.id)}
            selectable={bulkMode !== null}
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
        pageSize={DOCUMENT_PAGE_SIZE}
        onPageChange={(next) => updateQuery({ page: next })}
      />
    </>
  )
}
