import { useAsync } from '../hooks/useAsync'
import { type DocumentList } from '../hooks/useDocumentList'
import { type DocumentRecord } from '../lib/api/documents'
import { cardImage } from '../lib/documentPreview'
import { pb } from '../lib/pb'
import { useCardLayout } from '../lib/userSettings'
import { DocumentBulkBar, type BulkMode } from './DocumentBulkBar'
import { DocumentCard } from './DocumentCard'
import { Pagination } from './Pagination'

function cardImageUrl(document: DocumentRecord, token: string) {
  const image = cardImage(document)
  return image ? pb.files.getURL(document, image.name, { token, thumb: image.thumb }) : undefined
}

/**
 * A page of documents: the bulk bar, the cards and the pager, identical wherever
 * documents are listed.
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
    reprocessOverrides,
    setReprocessOverrides,
    reprocessing,
    markingReviewed,
    deleting,
    onReprocessSelected,
    onMarkReviewed,
    onAcceptSuggestedTag,
    acceptingSuggestion,
    onDeleteSelected,
    filterByTag,
    page,
    pageSize,
    changePageSize,
    totalPages,
    totalItems,
    updateQuery,
  } = list
  const layout = useCardLayout()
  // One token for the page: the previews are protected files like the originals.
  // ponytail: minted per page, so a card that first loads its image over 15
  // minutes later (the token's lifetime) shows a broken one; re-mint on error if that bites.
  const { data: fileToken } = useAsync(
    () => (layout === 'preview' ? pb.files.getToken({ requestKey: null }) : Promise.resolve('')),
    [layout, page],
  )

  return (
    <>
      {bulkMode && (
        <DocumentBulkBar
          mode={bulkMode}
          selectedCount={selectedOnPage.length}
          busy={reprocessing || markingReviewed || deleting}
          reprocessMode={reprocessMode}
          onReprocessModeChange={setReprocessMode}
          reprocessOverrides={reprocessOverrides}
          onReprocessOverridesChange={setReprocessOverrides}
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
            onFilterTag={filterByTag}
            onAcceptSuggestedTag={(id, name) => void onAcceptSuggestedTag(id, name)}
            acceptingSuggestion={acceptingSuggestion}
            previewUrl={fileToken ? cardImageUrl(document, fileToken) : undefined}
          />
        ))}
      </div>

      <Pagination
        page={page}
        totalPages={totalPages}
        totalItems={totalItems}
        pageSize={pageSize}
        onPageSizeChange={changePageSize}
        onPageChange={(next) => updateQuery({ page: next })}
      />
    </>
  )
}
