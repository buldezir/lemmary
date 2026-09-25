/**
 * The document statuses. Mirrors the `processing_status` SelectField in
 * backend/migrations/1730000001_initial.go, which rejects anything else.
 */

export const DOCUMENT_STATUSES = [
  'pending',
  'processing',
  'completed',
  'failed',
  'cancelled',
  'needs_review',
] as const

export type DocumentStatus = (typeof DOCUMENT_STATUSES)[number]

/** Filled badge and a card edged to match, except for the resting state. */
export const DOCUMENT_STATUS_STYLES: Record<DocumentStatus, { badge: string; border: string }> = {
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

export const DOCUMENT_STATUS_LABELS: Record<DocumentStatus, string> = {
  pending: 'Pending',
  processing: 'Processing',
  completed: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
  needs_review: 'Needs review',
}

/**
 * Not a document status: every status except completed. Mirrors
 * models.StatusFilterUnfinished, which the search endpoint understands too, so
 * both paths agree about what the Inbox holds.
 */
export const UNFINISHED_STATUS = 'unfinished'

export function isDocumentStatus(value: unknown): value is DocumentStatus {
  return typeof value === 'string' && (DOCUMENT_STATUSES as readonly string[]).includes(value)
}

/** Mirrors minExtractionConfidence in backend/internal/worker/step_extract_apply.go. */
export const LOW_CONFIDENCE_THRESHOLD = 0.5

export type ReviewReason = 'duplicate' | 'low_confidence' | 'awaiting'

/**
 * Why a document is waiting for review. With "always require review" on,
 * needs_review no longer implies low confidence or a duplicate.
 */
export function reviewReason(document: {
  duplicate_of?: string
  confidence?: number
}): ReviewReason {
  if (document.duplicate_of) {
    return 'duplicate'
  }
  const confidence = document.confidence ?? 0
  // Zero reads as "never recorded": a document that skipped extraction has no
  // confidence to be low.
  if (confidence > 0 && confidence < LOW_CONFIDENCE_THRESHOLD) {
    return 'low_confidence'
  }
  return 'awaiting'
}
