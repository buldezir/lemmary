/**
 * Whether this instance requires review of every document the AI touched.
 *
 * Module state with a setter, which this codebase otherwise avoids: the answer
 * has to be readable synchronously by the router's `validateSearch`, which
 * decides what a documents-list URL means before any component mounts and
 * cannot await anything. It arrives once with /api/app/meta and changes only
 * when an admin saves Settings.
 *
 * Defaults to off, which is the behaviour from before the Inbox existed.
 */

let alwaysRequireReview = false

/** Called from getAppMeta, the one place the answer arrives. */
export function setAlwaysRequireReview(value: boolean): void {
  alwaysRequireReview = value
}

export function requiresReview(): boolean {
  return alwaysRequireReview
}

/**
 * The list to return to when leaving a document, or when an upload finishes.
 * With review required, that is the Inbox: the one list showing what is still
 * to be worked through.
 */
export function documentsLanding(): '/' | '/inbox' {
  return alwaysRequireReview ? '/inbox' : '/'
}
