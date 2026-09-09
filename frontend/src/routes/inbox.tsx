import { UNFINISHED_STATUS } from '../lib/documentStatus'
import { useDocumentList } from '../hooks/useDocumentList'
import { DocumentGrid } from '../components/DocumentGrid'

/**
 * The working tray: everything the pipeline has not finished with -- waiting
 * for review, but also still queued and failed. A document that never came out
 * of the pipeline needs you as much as one that came out doubtful.
 *
 * Its own page rather than the documents list under a flag. The two share their
 * data (useDocumentList) and their rows (DocumentGrid), and nothing else: no
 * search, no filters, no timeline. A tray is worked through until it is empty,
 * not queried -- and narrowing it only hides work still to do. Searching and
 * filtering are what the documents list is for.
 */
export function InboxPage() {
  const list = useDocumentList({
    route: '/inbox',
    status: UNFINISHED_STATUS,
    filters: false,
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
          <div className="rounded-none border border-dashed border-line-strong bg-surface py-10 text-center">
            {/* A cleared Inbox is not empty in the sense of "upload something",
                so it says the opposite of what the documents list would. There
                is no filter to blame it on here. */}
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
