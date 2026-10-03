import { UNFINISHED_STATUS } from '../lib/documentStatus'
import { useDocumentList } from '../hooks/useDocumentList'
import { useAsync } from '../hooks/useAsync'
import { DocumentGrid } from '../components/DocumentGrid'
import { Button } from '../components/ui'
import {
  confirmReprocessFailed,
  countFailedDocuments,
  reprocessFailedBatch,
  reprocessFailedLabel,
} from '../lib/api/documents'
import { t } from '../i18n'

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
  // Counted rather than read off the page: the page is twelve documents, the
  // button reprocesses every failed one. Recounted whenever the list reloads.
  const failedCount = useAsync(() => countFailedDocuments(), [documents]).data ?? 0

  // Confirmed outside runBulkAction, so a dismissal keeps the selection and
  // the message and never shows the button as busy.
  function onReprocessFailed() {
    const batch = confirmReprocessFailed(failedCount)
    if (batch) {
      void list.runBulkAction(() => reprocessFailedBatch(batch), t('documents.reprocessFailed'))
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">{t('inbox.title')}</h2>
          <p className="text-sm text-ink-soft">{t('inbox.intro')}</p>
        </div>
        {failedCount > 0 && (
          <Button variant="secondary" size="xs" disabled={list.runningAction} onClick={onReprocessFailed}>
            {list.runningAction ? t('documentBulkBar.queueing') : reprocessFailedLabel(failedCount)}
          </Button>
        )}
      </div>

      <div className="flex flex-col gap-4">
        {loading && <p className="text-sm text-ink-soft">{t('index.loading')}</p>}
        {error && <p className="text-sm text-madder">{error}</p>}

        {!loading && documents.length === 0 && (
          <div className="rounded-none border border-line bg-surface py-10 text-center">
            <p className="text-sm text-ink-soft">{t('inbox.empty')}</p>
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
