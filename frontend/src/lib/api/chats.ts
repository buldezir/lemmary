import { apiFetch, ConnectionLostError, HttpError, sleep } from '../apiClient'
import { foldSteps, type ResearchStep, type StoredResearchStep } from '../researchSteps'
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
   * reopening restores the picker on what the transcript was produced with.
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
  /** Client-generated id of the request that produced this stored pair. */
  run_id?: string
  documents?: SearchDocumentHit[]
  /** Research trail as the stream emitted it. Empty on user turns and Search. */
  steps?: StoredResearchStep[]
  incomplete?: boolean
  created?: string
}

export type ChatSessionDetail = {
  session: ChatSession
  messages: ChatMessageRecord[]
  truncated?: boolean
  /**
   * A run is writing into this conversation right now. A turn is stored whole
   * when the run ends, so a chat opened mid-run reads as empty and finished
   * unless the server says otherwise.
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
  steps?: ResearchStep[]
  incomplete?: boolean
}

type ChatSessionListResponse = { items?: ChatSession[]; totalItems?: number }

/**
 * The server caps a listing at the sessions an account may hold, so one request
 * is the whole list. Asked explicitly rather than resting on the default.
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

export function getChatSession(id: string, signal?: AbortSignal): Promise<ChatSessionDetail> {
  return apiFetch<ChatSessionDetail>(`/api/app/chats/${encodeURIComponent(id)}`, {
    fallbackError: 'Failed to load the chat',
    signal,
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
 * Text is deliberately not the identity: two tabs can ask the same question,
 * and the last to finish would be presented as both tabs' answer. The
 * correlation id is stored with the pair atomically.
 */
export function storedAnswerForRun(
  messages: ChatMessageRecord[],
  runId: string,
): ChatMessageRecord | null {
  return (
    messages.findLast((message) => message.role === 'assistant' && message.run_id === runId) ?? null
  )
}

/** Worth another try: the wire, or a server that is failing rather than refusing. */
export function isRetryableFailure(err: unknown): boolean {
  return err instanceof ConnectionLostError || (err instanceof HttpError && err.status >= 500)
}

type PollAttempt<T> = { done: false } | { done: true; value: T | null }

/**
 * Transport failures and 5xx are retried: whatever broke the connection is
 * usually still broken, and one bad gateway in a twenty-minute wait must not
 * hand the question back while the run goes on. 4xx is terminal, since a
 * deleted empty session means no answer is coming.
 */
async function pollUntil<T>(
  attempt: () => Promise<PollAttempt<T>>,
  options: { signal?: AbortSignal; timeoutMs?: number; intervalMs?: number },
): Promise<T | null> {
  const interval = options.intervalMs ?? storedTurnPollMs
  const deadline = Date.now() + (options.timeoutMs ?? storedTurnWaitMs)
  for (;;) {
    if (options.signal?.aborted) {
      return null
    }
    try {
      const result = await attempt()
      if (result.done) {
        return result.value
      }
    } catch (err) {
      if (options.signal?.aborted) {
        return null
      }
      if (!isRetryableFailure(err)) {
        throw err
      }
    }
    if (Date.now() >= deadline) {
      return null
    }
    await sleep(interval, options.signal)
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
 * Waits for the turn a lost connection stopped this client from receiving: the
 * server finishes the run and stores the turn whether or not anyone listens.
 *
 * Every read looks for the request's correlation id, so this returns as soon as
 * this exact turn lands even while another tab writes into the conversation.
 * Resolves null when nothing landed: a run that failed, was cancelled,
 * outlived its budget, or never started.
 */
export async function waitForStoredTurn(
  sessionId: string,
  runId: string,
  options: WaitOptions = {},
): Promise<{ session: ChatSession; message: ChatMessageRecord } | null> {
  const load = options.load ?? ((id: string) => getChatSession(id, options.signal))
  return pollUntil<{ session: ChatSession; message: ChatMessageRecord }>(async () => {
    const detail = await load(sessionId)
    const message = storedAnswerForRun(detail.messages, runId)
    if (message) {
      return { done: true, value: { session: detail.session, message } }
    }
    return detail.running ? { done: false } : { done: true, value: null }
  }, options)
}

/**
 * Follows a conversation another run is writing into, until it ends. Reloading
 * during a research run abandons the stream but not the run, and the turn is
 * stored whole at the end, so the server reports the run and this waits it out.
 *
 * Resolves with the transcript once the run is over, which is the answer unless
 * the run stored nothing.
 */
export function waitWhileRunning(
  sessionId: string,
  options: WaitOptions = {},
): Promise<ChatSessionDetail | null> {
  const load = options.load ?? ((id: string) => getChatSession(id, options.signal))
  return pollUntil<ChatSessionDetail>(async () => {
    const detail = await load(sessionId)
    return detail.running ? { done: false } : { done: true, value: detail }
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

/**
 * Copies a conversation into one of its own, up to and including `upto`.
 * Without it the copy is the whole transcript, as the composer's fork is.
 */
export async function forkChatSession(id: string, upto?: string): Promise<ChatSession> {
  const data = await apiFetch<ChatSessionResponse>(
    `/api/app/chats/${encodeURIComponent(id)}/fork`,
    {
      method: 'POST',
      body: { upto: upto ?? '' },
      fallbackError: 'Failed to fork the chat',
    },
  )
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
 * Undefined for a session with no provider even if it carries a model: the
 * server refuses that pair, so offering it back would only produce a request
 * it will not accept.
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
 * Upserts a session into the list and re-sorts by activity, so a just-created
 * chat is in the rail before the background list reload lands.
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
 * `fallbackDocuments` covers the send response, where the hits ride alongside
 * the message rather than inside it. Empty stays `undefined` rather than `[]`
 * so the hit grid renders nothing instead of an empty row.
 */
export function toChatTurn(
  message: ChatMessageRecord,
  fallbackDocuments?: SearchDocumentHit[],
): ChatTurn {
  const documents = message.documents ?? fallbackDocuments
  const turn: ChatTurn = {
    id: message.id,
    role: message.role,
    content: message.content,
    documents: documents && documents.length > 0 ? documents : undefined,
  }
  const steps = foldSteps(message.steps)
  if (steps) turn.steps = steps
  if (message.incomplete) turn.incomplete = true
  return turn
}
