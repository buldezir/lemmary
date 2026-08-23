import { Link } from '@tanstack/react-router'
import type { DocumentRecord } from '../lib/api/documents'

type Props = {
  document: DocumentRecord
  selectable?: boolean
  selected?: boolean
  onToggleSelect?: (id: string) => void
}

const statusLabels: Record<DocumentRecord['processing_status'], string> = {
  pending: 'Pending',
  processing: 'Processing',
  completed: 'Completed',
  failed: 'Failed',
  needs_review: 'Needs review',
}

const statusStyles: Record<DocumentRecord['processing_status'], string> = {
  pending: 'text-amber-800 ring-amber-800/40',
  processing: 'text-sky-900 ring-sky-900/40',
  completed: 'text-forest ring-forest/40',
  failed: 'text-madder ring-madder/50',
  needs_review: 'text-amber-800 ring-amber-800/40',
}

function CardDescription({ document }: { document: DocumentRecord }) {
  const summary = document.summary?.trim() || document.purpose?.trim()
  if (summary) {
    return <p className="line-clamp-3 text-sm text-ink-muted">{summary}</p>
  }
  if (document.processing_status !== 'needs_review') {
    return <p className="line-clamp-3 text-sm text-ink-muted">No summary yet.</p>
  }
  if (document.duplicate_of) {
    const originalTitle = document.expand?.duplicate_of?.title?.trim() || 'another document'
    return (
      <p className="line-clamp-3 text-sm text-amber-800">
        Possible duplicate of{' '}
        <Link
          to="/document/$documentId"
          params={{ documentId: document.duplicate_of }}
          className="relative z-10 pointer-events-auto font-medium underline underline-offset-2 hover:text-amber-950"
        >
          {originalTitle}
        </Link>
        .
      </p>
    )
  }
  const pct = Math.round((document.confidence ?? 0) * 100)
  return (
    <p className="line-clamp-3 text-sm text-amber-800">Low extraction confidence ({pct}%).</p>
  )
}

export function DocumentCard({ document, selectable, selected, onToggleSelect }: Props) {
  const tags = document.expand?.tags?.map((tag) => tag.name) ?? []
  const correspondent = document.expand?.correspondent?.name
  const documentType = document.expand?.document_type?.name
  const title = document.title || 'Untitled document'

  return (
    <article
      data-document-id={document.id}
      className={`relative flex flex-col gap-3 border bg-surface p-5 transition-colors hover:border-ink/50 hover:bg-bright hover:shadow-sm hover:shadow-ink/10 ${
        selected ? 'border-oxblood ring-1 ring-oxblood' : 'border-line'
      }`}
    >
      <Link
        to="/document/$documentId"
        params={{ documentId: document.id }}
        aria-label={title}
        className="absolute inset-0 z-0 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood"
      />
      <div className="pointer-events-none relative flex flex-col gap-3">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            {/* Sits above the full-bleed link so ticking it does not navigate. */}
            {selectable && (
              <input
                type="checkbox"
                checked={Boolean(selected)}
                onChange={() => onToggleSelect?.(document.id)}
                aria-label={`Select ${title}`}
                className="relative z-10 pointer-events-auto h-4 w-4 cursor-pointer rounded border-line-strong text-oxblood focus:ring-oxblood"
              />
            )}
            <span
              className={`inline-flex px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ring-1 ring-inset ${statusStyles[document.processing_status]}`}
            >
              {statusLabels[document.processing_status]}
            </span>
          </div>
          {document.document_date && (
            <span className="font-mono text-xs tabular-nums text-ink-soft">{document.document_date.slice(0, 10)}</span>
          )}
        </div>

        <div className="border-t border-line pt-3">
          <h3 className="font-display text-lg font-semibold leading-snug text-ink">{title}</h3>
          <p className="mt-1 text-[11px] font-medium uppercase tracking-[0.08em] text-ink-soft">
            {[documentType || 'Unknown type', correspondent].filter(Boolean).join(' · ')}
          </p>
        </div>

        <CardDescription document={document} />

        {tags.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {tags.map((tag) => (
              <span key={tag} className="border border-line px-1.5 py-0.5 text-[11px] text-ink-muted">
                {tag}
              </span>
            ))}
          </div>
        )}
      </div>
    </article>
  )
}
