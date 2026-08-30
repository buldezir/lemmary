import { describe, expect, it } from 'vitest'
import {
  chatSessionDateLabel,
  chatSessionTitle,
  mergeChatSession,
  toChatTurn,
  type ChatSession,
} from './chats'

function session(overrides: Partial<ChatSession> = {}): ChatSession {
  return {
    id: 'a',
    kind: 'search',
    title: 'Plumbing invoice',
    message_count: 2,
    last_message_at: '2026-01-02 10:00:00.000Z',
    created: '2026-01-02 09:00:00.000Z',
    updated: '2026-01-02 10:00:00.000Z',
    ...overrides,
  }
}

describe('chatSessionTitle', () => {
  it('uses the stored title', () => {
    expect(chatSessionTitle(session())).toBe('Plumbing invoice')
  })

  it('falls back when the title is blank', () => {
    expect(chatSessionTitle(session({ title: '   ' }))).toBe('New chat')
  })
})

describe('chatSessionDateLabel', () => {
  it('trims a PocketBase timestamp to its date', () => {
    expect(chatSessionDateLabel('2026-01-02 10:00:00.000Z')).toBe('2026-01-02')
  })

  it('renders a dash when there is no timestamp', () => {
    expect(chatSessionDateLabel('')).toBe('—')
    expect(chatSessionDateLabel('   ')).toBe('—')
    expect(chatSessionDateLabel(undefined)).toBe('—')
  })
})

describe('mergeChatSession', () => {
  const older = session({ id: 'older', last_message_at: '2026-01-01 10:00:00.000Z' })
  const newer = session({ id: 'newer', last_message_at: '2026-01-03 10:00:00.000Z' })

  it('leaves the list alone when there is nothing to merge', () => {
    const list = [newer, older]
    expect(mergeChatSession(list, null)).toBe(list)
  })

  it('inserts a new session in activity order', () => {
    const merged = mergeChatSession([older], newer)
    expect(merged.map((item) => item.id)).toEqual(['newer', 'older'])
  })

  // The just-sent session arrives again from the background list reload; it has
  // to replace the row rather than appear beside it.
  it('replaces an existing session instead of duplicating it', () => {
    const updated = { ...older, title: 'Renamed' }
    const merged = mergeChatSession([newer, older], updated)
    expect(merged).toHaveLength(2)
    expect(merged.find((item) => item.id === 'older')?.title).toBe('Renamed')
  })

  it('moves a session to the top once its activity is newest', () => {
    const bumped = { ...older, last_message_at: '2026-01-04 10:00:00.000Z' }
    const merged = mergeChatSession([newer, older], bumped)
    expect(merged[0].id).toBe('older')
  })
})

describe('toChatTurn', () => {
  it('projects a stored message', () => {
    const turn = toChatTurn({ id: 'm1', role: 'assistant', content: 'Found it.' })
    expect(turn).toEqual({ id: 'm1', role: 'assistant', content: 'Found it.', documents: undefined })
  })

  it('prefers the hits stored on the message', () => {
    const turn = toChatTurn(
      { id: 'm1', role: 'assistant', content: 'x', documents: [{ id: 'stored', title: 'S' }] },
      [{ id: 'fallback', title: 'F' }],
    )
    expect(turn.documents?.[0].id).toBe('stored')
  })

  // The send response carries the hits beside the message rather than inside it.
  it('falls back to response-level hits', () => {
    const turn = toChatTurn({ id: 'm1', role: 'assistant', content: 'x' }, [
      { id: 'fallback', title: 'F' },
    ])
    expect(turn.documents?.[0].id).toBe('fallback')
  })

  // undefined rather than [], so the hit grid renders nothing at all instead of
  // an empty row under the answer.
  it('leaves an empty hit list undefined', () => {
    expect(toChatTurn({ id: 'm1', role: 'assistant', content: 'x' }, []).documents).toBeUndefined()
    expect(
      toChatTurn({ id: 'm1', role: 'assistant', content: 'x', documents: [] }).documents,
    ).toBeUndefined()
  })
})
