/**
 * Whether this instance requires review of every document the AI touched, and
 * the two things that answer changes.
 *
 * Module state with a setter, which the rest of this codebase avoids -- so why
 * here. The answer has to be readable *synchronously* by the router's
 * `validateSearch`, which decides what a documents-list URL looks like before
 * any component has mounted and cannot await anything. A hook cannot be called
 * from there, and route context is resolved after validateSearch runs. The
 * value itself is a per-instance setting that arrives once with /api/app/meta
 * and changes only when an admin saves Settings, so a single cell holding it is
 * the honest shape.
 *
 * It defaults to off, and off is the behaviour Lemmary had before the Inbox
 * existed: unknown must not narrow anybody's document list.
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
 * The status the documents list shows when its URL names none.
 *
 * With review required, everything the pipeline produces waits in the Inbox,
 * so an unfiltered list would be mostly a second copy of it. Completed is then
 * "the archive I have actually read", and the Inbox is the pile -- which is the
 * split that makes an inbox worth having.
 */
export function defaultStatusFilter(): 'all' | 'completed' {
  return alwaysRequireReview ? 'completed' : 'all'
}

/**
 * Where a freshly created batch of documents can actually be seen.
 *
 * Sending an upload to `/` would otherwise land the user on a list that filters
 * out exactly what they just uploaded.
 */
export function landingAfterUpload(): '/' | '/inbox' {
  return alwaysRequireReview ? '/inbox' : '/'
}
