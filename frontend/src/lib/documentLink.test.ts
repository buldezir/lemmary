import { describe, expect, it } from 'vitest'
import { documentLinkTarget } from './documentLink'

describe('documentLinkTarget', () => {
  it('recognises a citation', () => {
    expect(documentLinkTarget('/document/abc123')).toBe('abc123')
  })

  // The backend tolerates a page anchor on a citation, so the link has to keep
  // navigating in-app rather than falling through to an external anchor that
  // reloads the whole SPA.
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
