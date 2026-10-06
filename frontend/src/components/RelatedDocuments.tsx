import { useId, useState } from 'react'
import { Link } from '@tanstack/react-router'

import { Combobox } from './Combobox'
import { useAsync } from '../hooks/useAsync'
import {
  linkDocuments,
  listRelatedDocuments,
  searchLinkableDocuments,
  unlinkDocuments,
  type RelatedDocument,
} from '../lib/api/relatedDocuments'
import { t } from '../i18n'

function documentLabel(document: RelatedDocument): string {
  const title = document.title?.trim() || t('common.untitledDocument')
  return document.document_date ? `${title} · ${document.document_date.slice(0, 10)}` : title
}

/**
 * `version` is the document's updated stamp, so links the pipeline adds while
 * the page is open show up with the rest of its results.
 */
export function RelatedDocuments({
  documentId,
  owned,
  version,
}: {
  documentId: string
  owned: boolean
  version: string
}) {
  const related = useAsync(() => listRelatedDocuments(documentId), [documentId, version])
  const [query, setQuery] = useState('')
  const candidates = useAsync(
    () => (owned ? searchLinkableDocuments(documentId, query) : Promise.resolve([])),
    [documentId, owned, query],
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const headingId = useId()

  const linked = related.data ?? []
  if (!owned && linked.length === 0) return null

  const linkedIds = new Set(linked.map((document) => document.id))
  const options = (candidates.data ?? [])
    .filter((document) => !linkedIds.has(document.id))
    .map((document) => ({ value: document.id, label: documentLabel(document) }))

  async function change(action: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await action()
      await related.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section
      aria-labelledby={headingId}
      className="flex flex-col gap-3 rounded-none border border-line bg-surface p-5"
    >
      <h3 id={headingId} className="text-sm font-semibold text-ink">
        {t('documentPage.related')}
      </h3>
      {related.error && <p className="text-sm text-madder">{related.error}</p>}
      {!related.loading && !related.error && linked.length === 0 && (
        <p className="text-sm text-ink-soft">{t('documentPage.noRelated')}</p>
      )}
      {linked.length > 0 && (
        <ul className="flex flex-col gap-1.5">
          {linked.map((document) => (
            <li key={document.id} className="flex items-center justify-between gap-3 text-sm">
              <Link
                to="/document/$documentId"
                params={{ documentId: document.id }}
                className="truncate text-oxblood underline"
              >
                {documentLabel(document)}
              </Link>
              {owned && (
                <button
                  type="button"
                  disabled={busy}
                  aria-label={t('documentPage.unlinkRelated', { title: documentLabel(document) })}
                  className="text-ink-faint transition-colors hover:text-madder disabled:opacity-50"
                  onClick={() => void change(() => unlinkDocuments(documentId, document.id))}
                >
                  &times;
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {owned && (
        <Combobox
          value=""
          options={options}
          placeholder={t('documentPage.linkRelated')}
          ariaLabel={t('documentPage.linkRelated')}
          disabled={busy}
          className="max-w-md"
          onQueryChange={setQuery}
          onChange={(id) => {
            setQuery('')
            void change(() => linkDocuments(documentId, id))
          }}
        />
      )}
      {error && <p className="text-sm text-madder">{error}</p>}
    </section>
  )
}
