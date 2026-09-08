/**
 * Whether this instance requires review of every document the AI touched.
 *
 * Module state with a setter, which this codebase otherwise avoids: the answer
 * has to be readable synchronously by the router's `validateSearch`, which
 * decides what a documents-list URL means before any component mounts and
 * cannot await anything. It arrives once with /api/app/meta and changes only
 * when an admin saves Settings.
 *
 * Defaults to off, which is the behaviour from before the Inbox existed:
 * unknown must not narrow anybody's document list.
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
 * The status the documents list shows when its URL names none. With review
 * required, everything the pipeline produces waits in the Inbox, so an
 * unfiltered list would be mostly a second copy of it.
 */
export function defaultStatusFilter(): 'all' | 'completed' {
  return alwaysRequireReview ? 'completed' : 'all'
}

/**
 * The list to return to when leaving a document, or when an upload finishes.
 * With review required, `/` is the reviewed archive -- the one list that cannot
 * show what was just uploaded or is still being worked through.
 */
export function documentsLanding(): '/' | '/inbox' {
  return alwaysRequireReview ? '/inbox' : '/'
}
