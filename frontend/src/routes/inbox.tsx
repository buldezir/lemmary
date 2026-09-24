import { UNFINISHED_STATUS } from '../lib/documentStatus'
import { useDocumentList } from '../hooks/useDocumentList'
import { DocumentGrid } from '../components/DocumentGrid'

/**
 * The working tray: everything the pipeline has not finished with -- waiting for
 * review, but also still queued and failed. Deliberately has no search, filters
 * or timeline: narrowing a tray only hides work still to do.
 */
export function InboxPage() {
  const list = useDocumentList({
    route: '/inbox',
    status: UNFINISHED_STATUS,
    filters: false,
    ownerOnly: true,
  })
  const { documents, loading, error } = list

  return (
    <section className="flex flex-col gap-3">
      <div>
        <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">Inbox</h2>
        <p className="text-sm text-ink-soft">
          Everything the pipeline has not finished with: waiting for review, still processing, or
          failed.
        </p>
      </div>

      <div className="flex flex-col gap-4">
        {loading && <p className="text-sm text-ink-soft">Loading documents...</p>}
        {error && <p className="text-sm text-madder">{error}</p>}

        {!loading && documents.length === 0 && (
          <div className="rounded-none border border-line bg-surface py-10 text-center">
            <p className="text-sm text-ink-soft">Nothing waiting.</p>
          </div>
        )}

        {list.message && <p className="text-sm text-forest">{list.message}</p>}

        {/* Always review: markDocumentsReviewed narrows a selection to the
            documents actually waiting for it, so a mixed page cannot mark a
            failed one reviewed. */}
        {!loading && documents.length > 0 && (
          <DocumentGrid list={list} bulkMode="review" allowDelete />
        )}
      </div>
    </section>
  )
}
