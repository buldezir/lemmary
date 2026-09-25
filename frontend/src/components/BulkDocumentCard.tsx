import { Link } from '@tanstack/react-router'
import type { DocumentRecord } from '../lib/api/documents'
import { pb } from '../lib/pb'
import { DOCUMENT_STATUS_LABELS, DOCUMENT_STATUS_STYLES } from '../lib/documentStatus'
import { SharedChip } from './DocumentCard'

function NewTabIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-4 w-4"
      aria-hidden="true"
    >
      <path d="M14 4h6v6" />
      <path d="M20 4l-9 9" />
      <path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
    </svg>
  )
}

/**
 * A card that is a checkbox: clicking it selects rather than opens, so opening
 * is a separate link into a new tab and the selection survives it. A shared
 * document takes no bulk action, so it is shown but not selectable.
 */
export function BulkDocumentCard({
  document,
  selected,
  onToggleSelect,
}: {
  document: DocumentRecord
  selected: boolean
  onToggleSelect: (id: string) => void
}) {
  const title = document.title || 'Untitled document'
  const shared = Boolean(pb.authStore.record?.id) && document.user !== pb.authStore.record?.id
  const tags = document.expand?.tags ?? []
  const status = DOCUMENT_STATUS_STYLES[document.processing_status]
  const summary = document.summary?.trim() || document.purpose?.trim()
  const Wrapper = shared ? 'div' : 'label'

  return (
    <Wrapper
      data-document-id={document.id}
      className={`flex flex-col gap-2.5 border bg-surface p-4 transition-colors ${
        shared ? 'opacity-70' : 'cursor-pointer hover:bg-bright'
      } ${selected ? 'border-oxblood ring-1 ring-oxblood' : status.border}`}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          {!shared && (
            <input
              type="checkbox"
              checked={selected}
              onChange={() => onToggleSelect(document.id)}
              aria-label={`Select ${title}`}
              className="h-4 w-4 cursor-pointer rounded border-line-strong text-oxblood focus:ring-oxblood"
            />
          )}
          <span
            className={`inline-flex px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ${status.badge}`}
          >
            {DOCUMENT_STATUS_LABELS[document.processing_status]}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {document.document_date && (
            <span className="font-mono text-xs tabular-nums text-ink-soft">{document.document_date.slice(0, 10)}</span>
          )}
          <Link
            to="/document/$documentId"
            params={{ documentId: document.id }}
            target="_blank"
            rel="noopener"
            aria-label={`Open ${title} in a new tab`}
            title="Open in a new tab"
            className="text-ink-soft transition-colors hover:text-oxblood focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood"
          >
            <NewTabIcon />
          </Link>
        </div>
      </div>

      <div className="border-t border-line pt-2.5">
        <h3 className="font-display text-lg font-semibold leading-snug text-ink">{title}</h3>
        <p className="mt-1 text-[11px] font-medium uppercase tracking-[0.08em] text-ink-soft">
          {[document.expand?.document_type?.name || 'Unknown type', document.expand?.correspondent?.name]
            .filter(Boolean)
            .join(' · ')}
        </p>
      </div>

      <p className="line-clamp-3 text-sm text-ink-muted">{summary || 'No summary yet.'}</p>

      {(shared || tags.length > 0) && (
        <div className="flex flex-wrap gap-1.5">
          {shared && <SharedChip />}
          {tags.map((tag) => (
            <span
              key={tag.id}
              className="border border-line px-1.5 py-0.5 text-[11px] text-ink-muted"
              style={{ borderColor: tag.color || undefined }}
            >
              {tag.name}
            </span>
          ))}
        </div>
      )}
    </Wrapper>
  )
}
