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

/** A document attached to a question with @. */
export type DocumentMention = { id: string; title: string }

/** A title as it can sit inside a link's text: a bracket or newline would end it. */
export function mentionTitle(title: string): string {
  return title.replace(/[[\]\s]+/g, ' ').trim()
}

/**
 * A question with its attached documents after it, as the links the server
 * reads them by. splitAttachments takes it apart again.
 */
export function attachDocuments(question: string, documents: DocumentMention[]): string {
  if (documents.length === 0) return question
  return `${question}\n\n${documents.map((doc) => `[${doc.title}](/document/${doc.id})`).join(' ')}`
}

const attachedBlockPattern = /\n\n((?:\[[^\]\n]*\]\(\/document\/[A-Za-z0-9_-]+\) ?)+)$/
const attachedLinkPattern = /\[([^\]\n]*)\]\(\/document\/([A-Za-z0-9_-]+)\)/g

/** The question exactly as typed, and the documents attachDocuments put after it. */
export function splitAttachments(content: string): {
  question: string
  documents: DocumentMention[]
} {
  const block = attachedBlockPattern.exec(content)
  if (!block) return { question: content, documents: [] }
  return {
    question: content.slice(0, block.index),
    documents: [...block[1].matchAll(attachedLinkPattern)].map((m) => ({ title: m[1], id: m[2] })),
  }
}
