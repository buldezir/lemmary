import { describe, expect, it } from 'vitest'
import {
  attachDocuments,
  documentLinkTarget,
  mentionTitle,
  splitAttachments,
} from './documentLink'

describe('documentLinkTarget', () => {
  it('recognises a citation', () => {
    expect(documentLinkTarget('/document/abc123')).toBe('abc123')
  })

  // A citation carrying a page anchor still has to navigate in-app.
  it('tolerates a page anchor', () => {
    expect(documentLinkTarget('/document/abc123?page=7')).toBe('abc123')
  })

  it('leaves other links alone', () => {
    for (const href of [
      undefined,
      '',
      'https://example.com/document/abc123',
      '/documents/abc123',
      '/document/abc123?other=1',
      '/document/abc 123',
    ]) {
      expect(documentLinkTarget(href)).toBeNull()
    }
  })
})

describe('attachments', () => {
  const lease = { id: 'a1', title: 'Lease' }
  const lease2024 = { id: 'b2', title: 'Lease 2024' }

  it('puts the attached documents after the question as links', () => {
    expect(attachDocuments('compare these', [lease, lease2024])).toBe(
      'compare these\n\n[Lease](/document/a1) [Lease 2024](/document/b2)',
    )
    expect(attachDocuments('no documents', [])).toBe('no documents')
  })

  // The composer splits its value on every keystroke, so a trailing space or
  // an empty question has to survive the round trip.
  it('splits them back off the question exactly as it was typed', () => {
    for (const question of ['compare these', 'mid-word ', '']) {
      expect(splitAttachments(attachDocuments(question, [lease, lease2024]))).toEqual({
        question,
        documents: [lease, lease2024],
      })
    }
  })

  it('leaves a link inside the question in the question', () => {
    const content = 'is [Lease](/document/a1) still valid?'
    expect(splitAttachments(content)).toEqual({ question: content, documents: [] })
  })

  it('keeps a title inside the link text', () => {
    expect(mentionTitle(' Invoice [draft]\n2024 ')).toBe('Invoice draft 2024')
  })
})
