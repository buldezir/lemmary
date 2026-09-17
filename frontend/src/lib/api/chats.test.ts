import { describe, expect, it } from 'vitest'
import { ConnectionLostError, HttpError } from '../apiClient'
import {
  chatSessionDateLabel,
  chatSessionTitle,
  chatSessionsInMode,
  mayStillBeRunning,
  mergeChatSession,
  storedAnswerForRun,
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

describe('chatSessionsInMode', () => {
  const research = session({ id: 'research', mode: 'research' })
  const search = session({ id: 'search', mode: 'search' })
  const legacy = session({ id: 'legacy' })

  it('keeps only the sessions of that mode', () => {
    const rows = chatSessionsInMode([research, search, legacy], 'research')
    expect(rows.map((item) => item.id)).toEqual(['research'])
  })

  it('reads a session with no mode as search', () => {
    expect(chatSessionsInMode([research, search, legacy], 'search').map((item) => item.id)).toEqual([
      'search',
      'legacy',
    ])
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

  it('falls back to response-level hits', () => {
    const turn = toChatTurn({ id: 'm1', role: 'assistant', content: 'x' }, [
      { id: 'fallback', title: 'F' },
    ])
    expect(turn.documents?.[0].id).toBe('fallback')
  })

  it('leaves an empty hit list undefined', () => {
    expect(toChatTurn({ id: 'm1', role: 'assistant', content: 'x' }, []).documents).toBeUndefined()
    expect(
      toChatTurn({ id: 'm1', role: 'assistant', content: 'x', documents: [] }).documents,
    ).toBeUndefined()
  })

  it('folds stored research steps onto the turn', () => {
    const turn = toChatTurn({
      id: 'm1',
      role: 'assistant',
      content: '€412.',
      steps: [
        { kind: 'search', status: 'start', query: 'leak' },
        { kind: 'search', status: 'done', query: 'leak', count: 1 },
      ],
      incomplete: true,
    })
    expect(turn.steps).toEqual([{ kind: 'search', label: '“leak” — 1 document found', done: true }])
    expect(turn.incomplete).toBe(true)
  })
})

describe('storedAnswerForRun', () => {
  const older = {
    id: 'm2',
    role: 'assistant',
    content: '€412.',
    run_id: 'run-old',
  } as ChatMessageRecord
  const newer = {
    id: 'm4',
    role: 'assistant',
    content: '€480.',
    run_id: 'run-new',
  } as ChatMessageRecord

  it('finds the answer produced by the requested run', () => {
    expect(storedAnswerForRun([older, newer], 'run-old')).toBe(older)
    expect(storedAnswerForRun([older, newer], 'run-new')).toBe(newer)
  })

  it('does not confuse two answers to identical question text', () => {
    expect(storedAnswerForRun([older, newer], 'run-elsewhere')).toBeNull()
  })
})

describe('waitForStoredTurn', () => {
  const question = 'How much for the car?'
  const runID = 'run-current'
  const older = [
    { id: 'm1', role: 'user', content: question, run_id: 'run-old' },
    { id: 'm2', role: 'assistant', content: '€412.', run_id: 'run-old' },
  ] as ChatMessageRecord[]
  const newer = [
    ...older,
    { id: 'm3', role: 'user', content: question, run_id: runID },
    { id: 'm4', role: 'assistant', content: '€480.', run_id: runID },
  ] as ChatMessageRecord[]

  it('resolves once the run ends and the turn is there', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', runID, {
      intervalMs: 0,
      load: async () => {
        calls += 1
        return calls < 3
          ? { session: session(), messages: [], running: true }
          : { session: session(), messages: newer }
      },
    })
    expect(calls).toBe(3)
    expect(result?.message.content).toBe('€480.')
  })

  // Why question-matching alone is unsafe: the same question is already in the
  // transcript, answered, and that answer is not this one's.
  it('never hands back an earlier answer while the run is still going', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', runID, {
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

  it('returns its exact answer while another run is still active', async () => {
    const result = await waitForStoredTurn('s1', runID, {
      intervalMs: 0,
      load: async () => ({ session: session(), messages: newer, running: true }),
    })
    expect(result?.message.id).toBe('m4')
  })

  it('keeps asking through failures', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', runID, {
      intervalMs: 0,
      load: async () => {
        calls += 1
        if (calls === 1) {
          throw new ConnectionLostError(new TypeError('Failed to fetch'))
        }
        return { session: session(), messages: newer }
      },
    })
    expect(result?.message.content).toBe('€480.')
  })

  it('keeps asking through a 5xx', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', runID, {
      intervalMs: 0,
      load: async () => {
        calls += 1
        if (calls === 1) {
          throw new HttpError(502, 'Bad gateway')
        }
        return { session: session(), messages: newer }
      },
    })
    expect(result?.message.content).toBe('€480.')
  })

  // A chat discarded when its provider failed answers 404, which means the run
  // is over rather than that the interruption persists.
  it('does not retry a terminal HTTP failure until the run budget', async () => {
    let calls = 0
    const waiting = waitForStoredTurn('s1', runID, {
      intervalMs: 0,
      load: async () => {
        calls += 1
        throw new HttpError(404, 'Chat not found.')
      },
    })
    await expect(waiting).rejects.toThrow('Chat not found.')
    expect(calls).toBe(1)
  })

  it('gives up as soon as nothing is running and nothing landed', async () => {
    let calls = 0
    const result = await waitForStoredTurn('s1', runID, {
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
    const result = await waitForStoredTurn('s1', runID, {
      intervalMs: 0,
      timeoutMs: 0,
      load: async () => ({ session: session(), messages: [], running: true }),
    })
    expect(result).toBeNull()
  })

  // Cancelling must stop the wait, or the composer stays disabled for the
  // whole run budget.
  it('stops when the run is cancelled', async () => {
    const controller = new AbortController()
    controller.abort()
    const result = await waitForStoredTurn('s1', runID, {
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

describe('mayStillBeRunning', () => {
  it('suspects a run behind a failure the app never answered', () => {
    expect(mayStillBeRunning(new ConnectionLostError(new TypeError('Failed to fetch')))).toBe(true)
    expect(mayStillBeRunning(new HttpError(502, 'Bad Gateway'))).toBe(true)
  })

  it("takes a 5xx carrying the app's own detail as final", () => {
    expect(
      mayStillBeRunning(
        new HttpError(502, 'Provider error (429): The usage limit has been reached', true),
      ),
    ).toBe(false)
  })

  it('never suspects a 4xx', () => {
    expect(mayStillBeRunning(new HttpError(400, 'A message is required.', true))).toBe(false)
  })
})
