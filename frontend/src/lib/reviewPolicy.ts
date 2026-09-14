/**
 * Whether this instance requires review of every document the AI touched.
 *
 * Module state with a setter, which this codebase otherwise avoids: the router's
 * `validateSearch` has to read it synchronously, before any component mounts.
 * It arrives with /api/app/meta and defaults to off, the pre-Inbox behaviour.
 */

let alwaysRequireReview = false

/** Called from getAppMeta, the one place the answer arrives. */
export function setAlwaysRequireReview(value: boolean): void {
  alwaysRequireReview = value
}

export function requiresReview(): boolean {
  return alwaysRequireReview
}

/** Where to return after a document or an upload; the Inbox when review is required. */
export function documentsLanding(): '/' | '/inbox' {
  return alwaysRequireReview ? '/inbox' : '/'
}
