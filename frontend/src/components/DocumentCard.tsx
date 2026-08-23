import { Link } from '@tanstack/react-router'
import type { DocumentRecord } from '../lib/pocketbase'

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
  pending: 'bg-amber-50 text-amber-700 ring-amber-200',
  processing: 'bg-blue-50 text-blue-700 ring-blue-200',
  completed: 'bg-green-50 text-green-700 ring-green-200',
  failed: 'bg-red-50 text-red-700 ring-red-200',
  needs_review: 'bg-amber-50 text-amber-700 ring-amber-200',
}

function CardDescription({ document }: { document: DocumentRecord }) {
  const summary = document.summary?.trim() || document.purpose?.trim()
  if (summary) {
    return <p className="line-clamp-3 text-sm text-stone-600">{summary}</p>
  }
  if (document.processing_status !== 'needs_review') {
    return <p className="line-clamp-3 text-sm text-stone-600">No summary yet.</p>
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
      className={`relative flex flex-col gap-3 rounded-lg border bg-stone-50 p-4 transition-colors hover:border-stone-300 hover:bg-white hover:shadow-sm ${
        selected ? 'border-gray-900 ring-1 ring-gray-900' : 'border-stone-200'
      }`}
    >
      <Link
        to="/document/$documentId"
        params={{ documentId: document.id }}
        aria-label={title}
        className="absolute inset-0 z-0 rounded-lg"
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
                className="relative z-10 pointer-events-auto h-4 w-4 cursor-pointer rounded border-stone-300 text-gray-900 focus:ring-gray-900"
              />
            )}
            <span
              className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${statusStyles[document.processing_status]}`}
            >
              {statusLabels[document.processing_status]}
            </span>
          </div>
          {document.document_date && (
            <span className="text-xs text-stone-400">{document.document_date.slice(0, 10)}</span>
          )}
        </div>

        <div>
          <h3 className="font-medium text-stone-950">{title}</h3>
          <p className="text-xs text-stone-500">
            {[documentType || 'Unknown type', correspondent].filter(Boolean).join(' · ')}
          </p>
        </div>

        <CardDescription document={document} />

        {tags.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {tags.map((tag) => (
              <span key={tag} className="rounded-full bg-stone-200/70 px-2 py-0.5 text-xs text-stone-600">
                {tag}
              </span>
            ))}
          </div>
        )}
      </div>
    </article>
  )
}
