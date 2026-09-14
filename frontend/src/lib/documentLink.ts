/**
 * Research answers cite documents as /document/<id> links, which have to
 * navigate in-app rather than reload the whole SPA. ?page=N is tolerated
 * because a model may add one though no OCR provider preserves page
 * boundaries.
 */
const documentLinkPattern = /^\/document\/([A-Za-z0-9_-]+)(?:\?page=\d+)?$/

/** The document a citation points at, or null when the link is an ordinary one. */
export function documentLinkTarget(href: string | undefined): string | null {
  return href?.match(documentLinkPattern)?.[1] ?? null
}
