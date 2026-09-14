import { describe, expect, it } from 'vitest'
import {
  chatSessionDateLabel,
  chatSessionTitle,
  mergeChatSession,
  storedAnswerTo,
  toChatTurn,
  waitForStoredTurn,
  waitWhileRunning,
  type ChatMessageRecord,
  type ChatSession,
  type ChatSessionDetail,
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

describe('storedAnswerTo', () => {
  const asked = { id: 'm1', role: 'user', content: 'How much for the car?' } as ChatMessageRecord
  const answered = { id: 'm2', role: 'assistant', content: '€412.' } as ChatMessageRecord

  it('finds the answer to the question that was asked', () => {
    expect(storedAnswerTo([asked, answered], 'How much for the car?')).toBe(answered)
  })

  it('is null while only the earlier turns are stored', () => {
    expect(storedAnswerTo([asked, answered], 'And the bike?')).toBeNull()
  })

  // A repeated question resolves to the newer answer: that is the run that was
  // being waited for, and the older one is already on screen.
  it('prefers the newest matching turn', () => {
    const again = { id: 'm4', role: 'assistant', content: '€480.' } as ChatMessageRecord
    const messages = [asked, answered, { ...asked, id: 'm3' }, again]
    expect(storedAnswerTo(messages, 'How much for the car?')).toBe(again)
  })

  // The question is stored with its answer in one write, so a transcript that
  // ends on the question means the run has not finished.
  it('ignores a question with nothing after it', () => {
    expect(storedAnswerTo([asked], 'How much for the car?')).toBeNull()
  })
})

describe('waitForStoredTurn', () => {
  const question = 'How much for the car?'
  const older = [
    { id: 'm1', role: 'user', content: question },
    { id: 'm2', role: 'assistant', content: '€412.' },
  ] as ChatMessageRecord[]
  const newer = [
    ...older,
    { id: 'm3', role: 'user', content: question },
    { id: 'm4', role: 'assistant', content: '€480.' },
  ] as ChatMessageRecord[]

  // The run kept going after the connection died, so the answer turns up in the
  // transcript a while later and has to be collected.
  it('resolves once the run ends and the turn is there', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', question, {
      intervalMs: 0,
      load: async () => {
        calls += 1
        return calls < 3
          ? { session: session(), messages: [], running: true }
          : { session: session(), messages: older }
      },
    })
    expect(calls).toBe(3)
    expect(result?.message.content).toBe('€412.')
  })

  // The failure that makes question-matching alone unsafe: the same question is
  // already in the transcript, answered. Until the run in flight ends, its
  // older answer must not be handed back as this one's.
  it('never hands back an earlier answer while the run is still going', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', question, {
      intervalMs: 0,
      load: async () => {
        calls += 1
        return calls < 3
          ? { session: session(), messages: older, running: true }
          : { session: session(), messages: newer }
      },
    })
    expect(result?.message.content).toBe('€480.')
  })

  // The connection that broke is usually still broken; giving up on the first
  // failed poll would lose exactly the answer we came back for.
  it('keeps asking through failures', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', question, {
      intervalMs: 0,
      load: async () => {
        calls += 1
        if (calls === 1) throw new Error('Could not reach the server.')
        return { session: session(), messages: older }
      },
    })
    expect(result?.message.content).toBe('€412.')
  })

  // A request that never reached the server leaves nothing running, so there is
  // nothing to wait for: say so at once instead of sitting out the budget.
  it('gives up as soon as nothing is running and nothing landed', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', question, {
      intervalMs: 0,
      load: async () => {
        calls += 1
        return { session: session(), messages: [] }
      },
    })
    expect(calls).toBe(1)
    expect(result).toBeNull()
  })

  it('gives up at the deadline', async () => {
    const result = await waitForStoredTurn('s1', question, {
      intervalMs: 0,
      timeoutMs: 0,
      load: async () => ({ session: session(), messages: [], running: true }),
    })
    expect(result).toBeNull()
  })

  // Cancelling has to stop the wait too, or the composer stays disabled until
  // the run budget runs out.
  it('stops when the run is cancelled', async () => {
    const controller = new AbortController()
    controller.abort()
    const result = await waitForStoredTurn('s1', question, {
      intervalMs: 0,
      signal: controller.signal,
      load: async () => ({ session: session(), messages: older }),
    })
    expect(result).toBeNull()
  })
})

describe('waitWhileRunning', () => {
  const answered: ChatSessionDetail = {
    session: session(),
    messages: [
      { id: 'm1', role: 'user', content: 'How much for the car?' },
      { id: 'm2', role: 'assistant', content: '€412.' },
    ],
  }

  // Reloading during a research run leaves a chat that looks empty and
  // finished. Only the server knows better, and this is what waits it out.
  it('waits for the run to end, then hands back the transcript', async () => {
    let calls = 0
    const result = await waitWhileRunning('s1', {
      intervalMs: 0,
      load: async () => {
        calls += 1
        return calls < 3 ? { session: session(), messages: [], running: true } : answered
      },
    })
    expect(calls).toBe(3)
    expect(result?.messages).toHaveLength(2)
  })

  it('returns straight away when nothing is running', async () => {
    expect(await waitWhileRunning('s1', { intervalMs: 0, load: async () => answered })).toBe(
      answered,
    )
  })

  it('gives up at the deadline', async () => {
    const result = await waitWhileRunning('s1', {
      intervalMs: 0,
      timeoutMs: 0,
      load: async () => ({ session: session(), messages: [], running: true }),
    })
    expect(result).toBeNull()
  })
})
