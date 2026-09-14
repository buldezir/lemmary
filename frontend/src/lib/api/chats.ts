import { apiFetch, sleep } from '../apiClient'
import type { ProviderBinding } from './providers'

export type ChatSessionKind = 'search' | 'document'
export type ChatRole = 'user' | 'assistant'

/** A document the search agent cited, captured with the answer that cited it. */
export type SearchDocumentHit = {
  id: string
  title: string
  document_date?: string
  summary?: string
  /** The best passage, shortened for display. */
  ocr_snippet?: string
  /**
   * Verbatim passages from the document that matched. `page` is set only when
   * the extraction preserved page boundaries, which no current OCR provider
   * does -- so it is absent in practice today.
   */
  passages?: { page?: number; text: string }[]
  document_type?: string
  correspondent?: string
  tags?: string[]
}

export type ChatSession = {
  id: string
  kind: ChatSessionKind
  title: string
  /** The mode the last search turn ran in; absent for document chats. Mirrors SearchMode. */
  mode?: 'search' | 'research'
  /**
   * The provider row and model this conversation is pinned to, absent when it
   * runs on the binding in Settings. Fixed for the conversation's lifetime, so
   * reopening a chat restores the picker on what its transcript was produced
   * with.
   */
  provider?: string
  model?: string
  /** Set only for kind === 'document'. */
  document?: string
  document_title?: string
  message_count: number
  last_message_at: string
  created: string
  updated: string
}

export type ChatMessageRecord = {
  id: string
  seq?: number
  role: ChatRole
  content: string
  documents?: SearchDocumentHit[]
  created?: string
}

export type ChatSessionDetail = {
  session: ChatSession
  messages: ChatMessageRecord[]
  truncated?: boolean
  /**
   * A run is writing into this conversation right now. Nothing in the
   * transcript can say so -- a turn is stored whole when the run ends -- so a
   * chat opened mid-run reads as empty and finished unless the server says
   * otherwise.
   */
  running?: boolean
}

/** One rendered row of a transcript. */
export type ChatTurn = {
  /** A record id, or `pending-N` for an optimistic bubble not yet saved. */
  id: string
  role: ChatRole
  content: string
  documents?: SearchDocumentHit[]
}

type ChatSessionListResponse = { items?: ChatSession[]; totalItems?: number }

/**
 * How many chats the rail asks for. The server caps a listing at the same
 * number of sessions an account may hold, so one request is always the whole
 * list and the sidebar scrolls rather than paging — asking explicitly keeps
 * that from resting on whatever the server's default happens to be.
 */
const chatListPageSize = 500
type ChatSessionResponse = { session: ChatSession }

export async function listChatSessions(params?: {
  kind?: ChatSessionKind
  documentId?: string
}): Promise<ChatSession[]> {
  const query = new URLSearchParams()
  if (params?.kind) {
    query.set('kind', params.kind)
  }
  if (params?.documentId) {
    query.set('document', params.documentId)
  }
  query.set('perPage', String(chatListPageSize))
  const data = await apiFetch<ChatSessionListResponse>(`/api/app/chats?${query.toString()}`, {
    fallbackError: 'Failed to load chats',
  })
  return data.items ?? []
}

export function getChatSession(id: string): Promise<ChatSessionDetail> {
  return apiFetch<ChatSessionDetail>(`/api/app/chats/${encodeURIComponent(id)}`, {
    fallbackError: 'Failed to load the chat',
  })
}

/** How often an interrupted run's conversation is re-read while waiting. */
const storedTurnPollMs = 3000

/**
 * How long to keep waiting. The server gives a detached run 20 minutes and
 * stops it there, so anything past that has no turn coming.
 */
const storedTurnWaitMs = 21 * 60 * 1000

/**
 * The answer to `question` in a transcript, or null if it is not there yet.
 *
 * Matched on the question rather than on a message count, because the count a
 * client remembers can be a turn out of date and would then hand back the
 * *previous* answer as the reply to this one. A turn is stored as one
 * user+assistant pair, so the newest user message carrying this question has
 * our answer directly after it -- and asking the same thing twice resolves to
 * the newer answer, which is the one that was waited for. Only ever read once
 * the run is over, or a repeat would match the older pair while the answer to
 * this one was still being written; waitForStoredTurn is what enforces that.
 *
 * The question is compared verbatim: the server only trims it, and the composer
 * has already done that.
 */
export function storedAnswerTo(
  messages: ChatMessageRecord[],
  question: string,
): ChatMessageRecord | null {
  for (let i = messages.length - 1; i > 0; i--) {
    const answer = messages[i]
    if (answer.role !== 'assistant') {
      continue
    }
    const asked = messages[i - 1]
    if (asked.role === 'user' && asked.content === question) {
      return answer
    }
  }
  return null
}

/**
 * Asks repeatedly until `attempt` produces something, or the deadline passes.
 *
 * Failures are swallowed and retried on purpose: whatever broke the connection
 * in the first place is usually still broken, and giving up on the first bad
 * poll loses exactly the answer this came back for.
 */
async function pollUntil<T>(
  attempt: () => Promise<T | null>,
  options: { signal?: AbortSignal; timeoutMs?: number; intervalMs?: number },
): Promise<T | null> {
  const interval = options.intervalMs ?? storedTurnPollMs
  const deadline = Date.now() + (options.timeoutMs ?? storedTurnWaitMs)
  for (;;) {
    if (options.signal?.aborted) {
      return null
    }
    try {
      const found = await attempt()
      if (found) {
        return found
      }
    } catch {
      // Still unreachable, or not finished. Either way, ask again.
    }
    if (Date.now() >= deadline) {
      return null
    }
    await sleep(interval)
  }
}

/** Options shared by the two waits; `load` is swapped out in tests. */
export type WaitOptions = {
  signal?: AbortSignal
  timeoutMs?: number
  intervalMs?: number
  load?: (id: string) => Promise<ChatSessionDetail>
}

/**
 * Waits for the turn a lost connection stopped this client from receiving.
 *
 * The run does not stop when the connection does -- the server finishes it and
 * stores the turn whether or not anyone is still listening. Until now nobody
 * went back for it: the page reported a lost connection and the answer sat in
 * the transcript, invisible until a manual reload, which on a follow-up
 * question looks exactly like the work having been thrown away.
 *
 * The run is waited out *first*, and only then is the transcript read. That
 * ordering is what makes a repeated question safe: asking the same thing twice
 * would otherwise match the earlier pair on the first poll and paint last
 * week's answer as this one's, while the real run was still working. It is
 * also what ends the wait quickly when the request never reached the server at
 * all -- nothing is running, so there is nothing to wait for.
 *
 * Resolves null when nothing landed: a run that failed, was cancelled, outlived
 * its budget, or never started.
 */
export async function waitForStoredTurn(
  sessionId: string,
  question: string,
  options: WaitOptions = {},
): Promise<{ session: ChatSession; message: ChatMessageRecord } | null> {
  const detail = await waitWhileRunning(sessionId, options)
  if (!detail) {
    return null
  }
  const message = storedAnswerTo(detail.messages, question)
  return message ? { session: detail.session, message } : null
}

/**
 * Follows a conversation somebody else's run is writing into, until it ends.
 *
 * That somebody is usually the same user a moment ago: reloading the page
 * during a research run abandons the stream but not the run, and the chat that
 * comes back is empty, with nothing to say an answer is on its way. The
 * transcript cannot show it -- the turn is stored whole at the end -- so the
 * server reports it and this waits it out.
 *
 * Resolves with the transcript as it stands once the run is over, which is the
 * answer unless the run failed and stored nothing.
 */
export function waitWhileRunning(
  sessionId: string,
  options: WaitOptions = {},
): Promise<ChatSessionDetail | null> {
  const load = options.load ?? getChatSession
  return pollUntil(async () => {
    const detail = await load(sessionId)
    return detail.running ? null : detail
  }, options)
}

export async function renameChatSession(id: string, title: string): Promise<ChatSession> {
  const data = await apiFetch<ChatSessionResponse>(`/api/app/chats/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: { title },
    fallbackError: 'Failed to rename the chat',
  })
  return data.session
}

export function deleteChatSession(id: string) {
  return apiFetch<unknown>(`/api/app/chats/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    fallbackError: 'Failed to delete the chat',
  })
}

/** What the sidebar shows for a session whose title never resolved. */
export function chatSessionTitle(session: ChatSession): string {
  return session.title.trim() || 'New chat'
}

/**
 * The binding a conversation is pinned to, as the picker wants it, or undefined
 * when it runs on the configured model.
 *
 * Undefined for a session with no provider even if it somehow carries a model:
 * the server refuses that pair, so offering it back as a choice would only
 * produce a request it will not accept.
 */
export function chatSessionBinding(session: ChatSession | null): ProviderBinding | undefined {
  const providerId = session?.provider?.trim()
  if (!providerId) return undefined
  return { provider_id: providerId, model: session?.model?.trim() ?? '' }
}

/** Trims a PocketBase timestamp to its date for the sidebar. */
export function chatSessionDateLabel(value: string | undefined): string {
  const trimmed = (value ?? '').trim()
  if (!trimmed) {
    return '—'
  }
  return trimmed.slice(0, 10)
}

/**
 * Upserts a session into the list and re-sorts by activity.
 *
 * Used to show a just-created chat in the rail immediately, before the
 * background list reload lands — without it the new row appears a round trip
 * late, after the transcript it belongs to is already on screen.
 */
export function mergeChatSession(
  sessions: ChatSession[],
  session: ChatSession | null,
): ChatSession[] {
  if (!session) {
    return sessions
  }
  const next = sessions.filter((item) => item.id !== session.id)
  next.push(session)
  next.sort((a, b) => {
    const byActivity = (b.last_message_at ?? '').localeCompare(a.last_message_at ?? '')
    return byActivity !== 0 ? byActivity : b.id.localeCompare(a.id)
  })
  return next
}

/**
 * Projects a stored message into a transcript row.
 *
 * `fallbackDocuments` covers the send response, where the hits ride alongside
 * the message rather than inside it. Empty stays `undefined` rather than `[]`
 * so the hit grid renders nothing at all instead of an empty row.
 */
export function toChatTurn(
  message: ChatMessageRecord,
  fallbackDocuments?: SearchDocumentHit[],
): ChatTurn {
  const documents = message.documents ?? fallbackDocuments
  return {
    id: message.id,
    role: message.role,
    content: message.content,
    documents: documents && documents.length > 0 ? documents : undefined,
  }
}
