import { Link } from '@tanstack/react-router'
import type { DocumentRecord } from '../lib/api/documents'
import { DOCUMENT_STATUS_LABELS, reviewReason, type DocumentStatus } from '../lib/documentStatus'
import { summarizeJob, type ProcessingJobRecord } from '../lib/processing'
import { pendingTagSuggestions } from '../lib/tagSuggestions'
import { ProcessingStatus } from './ProcessingStatus'
import { SuggestedTags } from './SuggestedTags'
import { Button } from './ui'

type Props = {
  document: DocumentRecord
  selectable?: boolean
  selected?: boolean
  onToggleSelect?: (id: string) => void
  /** Omit to hide the button; it shows only for a document that is waiting. */
  onMarkReviewed?: (id: string) => void
  /** Omit to leave the tag chips as plain text, on a list with no tags filter. */
  onFilterTag?: (tagId: string) => void
  markingReviewed?: boolean
  /**
   * The newest processing job of a failed or waiting document, when the list
   * fetched one. It says what the status badge cannot: which step failed and
   * why, or which tags the AI proposed for the reviewer.
   */
  job?: ProcessingJobRecord
  /** Omit to hide the AI's tag suggestions. */
  onAcceptSuggestedTag?: (id: string, name: string, currentTagIds: string[]) => void
}

/** Filled badge and a card edged to match, except for the resting state. */
const statusStyles: Record<DocumentStatus, { badge: string; border: string }> = {
  pending: { badge: 'bg-amber-800 text-paper', border: 'border-amber-800' },
  processing: { badge: 'bg-sky-900 text-paper', border: 'border-sky-900' },
  completed: {
    badge: 'text-forest ring-1 ring-inset ring-forest/40',
    border: 'border-line hover:border-ink/50',
  },
  failed: { badge: 'bg-madder text-paper', border: 'border-madder' },
  cancelled: { badge: 'bg-ink-muted text-paper', border: 'border-ink-muted' },
  needs_review: { badge: 'bg-amber-800 text-paper', border: 'border-amber-800' },
}

function CardDescription({ document }: { document: DocumentRecord }) {
  const summary = document.summary?.trim() || document.purpose?.trim()
  if (summary) {
    return <p className="line-clamp-3 text-sm text-ink-muted">{summary}</p>
  }
  if (document.processing_status !== 'needs_review') {
    return <p className="line-clamp-3 text-sm text-ink-muted">No summary yet.</p>
  }

  // With no summary to show, say why the document is waiting instead.
  switch (reviewReason(document)) {
    case 'duplicate': {
      const originalTitle = document.expand?.duplicate_of?.title?.trim() || 'another document'
      return (
        <p className="line-clamp-3 text-sm text-amber-800">
          Possible duplicate of{' '}
          <Link
            to="/document/$documentId"
            params={{ documentId: document.duplicate_of! }}
            className="relative z-10 pointer-events-auto font-medium underline underline-offset-2 hover:text-amber-950"
          >
            {originalTitle}
          </Link>
          .
        </p>
      )
    }
    case 'low_confidence': {
      const pct = Math.round((document.confidence ?? 0) * 100)
      return (
        <p className="line-clamp-3 text-sm text-amber-800">Low extraction confidence ({pct}%).</p>
      )
    }
    default:
      return <p className="line-clamp-3 text-sm text-amber-800">Waiting for review.</p>
  }
}

export function DocumentCard({
  document,
  selectable,
  selected,
  onToggleSelect,
  onMarkReviewed,
  markingReviewed,
  onFilterTag,
  job,
  onAcceptSuggestedTag,
}: Props) {
  const tags = document.expand?.tags ?? []
  const suggestions =
    onAcceptSuggestedTag && document.processing_status === 'needs_review'
      ? pendingTagSuggestions(job, tags.map((tag) => tag.name))
      : []
  const correspondent = document.expand?.correspondent?.name
  const documentType = document.expand?.document_type?.name
  const title = document.title || 'Untitled document'
  const canMarkReviewed = Boolean(onMarkReviewed) && document.processing_status === 'needs_review'
  const status = statusStyles[document.processing_status]

  return (
    <article
      data-document-id={document.id}
      className={`relative flex flex-col border bg-surface p-4 transition-colors hover:bg-bright hover:shadow-sm hover:shadow-ink/10 ${
        selected ? 'border-oxblood ring-1 ring-oxblood' : status.border
      }`}
    >
      <Link
        to="/document/$documentId"
        params={{ documentId: document.id }}
        aria-label={title}
        className="absolute inset-0 z-0 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood"
      />
      <div className="pointer-events-none relative flex flex-col gap-2.5">
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
              className={`inline-flex px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ${status.badge}`}
            >
              {DOCUMENT_STATUS_LABELS[document.processing_status]}
            </span>
          </div>
          {document.document_date && (
            <span className="font-mono text-xs tabular-nums text-ink-soft">{document.document_date.slice(0, 10)}</span>
          )}
        </div>

        <div className="border-t border-line pt-2.5">
          <h3 className="font-display text-lg font-semibold leading-snug text-ink">{title}</h3>
          <p className="mt-1 text-[11px] font-medium uppercase tracking-[0.08em] text-ink-soft">
            {[documentType || 'Unknown type', correspondent].filter(Boolean).join(' · ')}
          </p>
        </div>

        <CardDescription document={document} />

        <ProcessingStatus summary={summarizeJob(job)} />

        {tags.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {tags.map((tag) =>
              onFilterTag ? (
                // Above the full-bleed link, like the checkbox, so a click
                // filters the list instead of opening the document.
                <button
                  key={tag.id}
                  type="button"
                  aria-label={`Filter by ${tag.name}`}
                  className="relative z-10 pointer-events-auto border border-line px-1.5 py-0.5 text-[11px] text-ink-muted transition-colors hover:border-ink hover:text-ink"
                  style={{ borderColor: tag.color || undefined }}
                  onClick={() => onFilterTag(tag.id)}
                >
                  {tag.name}
                </button>
              ) : (
                <span
                  key={tag.id}
                  className="border border-line px-1.5 py-0.5 text-[11px] text-ink-muted"
                  style={{ borderColor: tag.color || undefined }}
                >
                  {tag.name}
                </span>
              ),
            )}
          </div>
        )}

        {/* Above the full-bleed link, like the tag filter chips. */}
        <SuggestedTags
          names={suggestions}
          disabled={markingReviewed}
          className="relative z-10 pointer-events-auto"
          onAccept={(name) => onAcceptSuggestedTag?.(document.id, name, document.tags ?? [])}
        />

        {canMarkReviewed && (
          <div className="flex justify-end">
            {/* Above the full-bleed link, like the checkbox, so a click clears
                the document instead of opening it. */}
            <Button
              variant="secondary"
              size="xs"
              disabled={markingReviewed}
              onClick={() => onMarkReviewed?.(document.id)}
              className="relative z-10 pointer-events-auto"
            >
              {markingReviewed ? 'Marking...' : 'Mark reviewed'}
            </Button>
          </div>
        )}
      </div>
    </article>
  )
}
