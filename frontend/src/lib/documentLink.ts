/**
 * Research answers cite documents as /document/<id> links. Those have to
 * navigate in-app: opening a new tab to reload the whole SPA for a citation
 * would make following one prohibitive.
 *
 * The optional ?page=N is tolerated rather than required. Nothing asks the
 * model for page numbers yet -- no OCR provider preserves page boundaries --
 * but a model that adds one anyway must still produce a working link rather
 * than an external one that reloads the app.
 */
const documentLinkPattern = /^\/document\/([A-Za-z0-9_-]+)(?:\?page=\d+)?$/

/** The document a citation points at, or null when the link is an ordinary one. */
export function documentLinkTarget(href: string | undefined): string | null {
  return href?.match(documentLinkPattern)?.[1] ?? null
}
