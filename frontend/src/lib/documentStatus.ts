/**
 * The document statuses, in one place.
 *
 * These five values mirror the `processing_status` SelectField in
 * backend/migrations/1730000001_initial.go -- the database rejects anything
 * else, so this list copies a constraint rather than a convention. It used to
 * be written out four times (the record type, the URL filter's allowlist, the
 * card's labels, the filter dropdown's options), and the copies had already
 * drifted in order.
 *
 * Tailwind class maps deliberately stay in the components: this module is
 * imported by lib/ code that has no business knowing how a badge looks.
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

/** Narrows an untrusted value -- a hand-edited URL, an older build's link. */
export function isDocumentStatus(value: unknown): value is DocumentStatus {
  return typeof value === 'string' && (DOCUMENT_STATUSES as readonly string[]).includes(value)
}

/**
 * Mirrors minExtractionConfidence in
 * backend/internal/worker/step_extract_apply.go: below this the pipeline sends
 * a document for review on its own. The frontend reads it only to explain
 * *why* a document is waiting, so drift between the two shows up as wrong
 * wording rather than a wrong status.
 */
export const LOW_CONFIDENCE_THRESHOLD = 0.5

/**
 * Why a document is waiting for review. Until the "always require review"
 * setting existed, needs_review implied one of the first two reasons, so the
 * card could assume low confidence whenever there was no duplicate to point
 * at. A confident document can now be waiting for no reason but the user's own
 * policy, and calling that "low extraction confidence" would be a lie.
 */
export type ReviewReason = 'duplicate' | 'low_confidence' | 'awaiting'

export function reviewReason(document: {
  duplicate_of?: string
  confidence?: number
}): ReviewReason {
  if (document.duplicate_of) {
    return 'duplicate'
  }
  const confidence = document.confidence ?? 0
  // Zero reads as "never recorded" rather than "certainly wrong": a document
  // that skipped extraction has no confidence to be low.
  if (confidence > 0 && confidence < LOW_CONFIDENCE_THRESHOLD) {
    return 'low_confidence'
  }
  return 'awaiting'
}
