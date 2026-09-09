/**
 * The document statuses. Mirrors the `processing_status` SelectField in
 * backend/migrations/1730000001_initial.go, which rejects anything else.
 */

export const DOCUMENT_STATUSES = [
  'pending',
  'processing',
  'completed',
  'failed',
  'needs_review',
] as const

export type DocumentStatus = (typeof DOCUMENT_STATUSES)[number]

export const DOCUMENT_STATUS_LABELS: Record<DocumentStatus, string> = {
  pending: 'Pending',
  processing: 'Processing',
  completed: 'Completed',
  failed: 'Failed',
  needs_review: 'Needs review',
}

/**
 * The Inbox's status filter, which is not a document status: it means every
 * status except completed -- what the pipeline has not finished with, plus what
 * it finished with badly. Mirrors models.StatusFilterUnfinished in
 * backend/internal/models/status.go, which the search endpoint understands too,
 * so the two paths agree about what the Inbox holds.
 */
export const UNFINISHED_STATUS = 'unfinished'

export function isDocumentStatus(value: unknown): value is DocumentStatus {
  return typeof value === 'string' && (DOCUMENT_STATUSES as readonly string[]).includes(value)
}

/** Mirrors minExtractionConfidence in backend/internal/worker/step_extract_apply.go. */
export const LOW_CONFIDENCE_THRESHOLD = 0.5

export type ReviewReason = 'duplicate' | 'low_confidence' | 'awaiting'

/**
 * Why a document is waiting for review. Until the "always require review"
 * setting existed, needs_review implied low confidence or a duplicate, so the
 * card could assume the former whenever there was no duplicate to point at.
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
