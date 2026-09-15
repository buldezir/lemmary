import { apiFetch, apiStream } from '../apiClient'
import { bindingBody, type ProviderBinding } from './providers'
import type { ChatMessageRecord, ChatSession, SearchDocumentHit } from './chats'

export type { SearchDocumentHit } from './chats'

/** The minimal message shape the transcript renderer needs. */
export type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
}

/**
 * `search` finds documents and lists them as cards. `research` reads them and
 * writes a cited answer, streaming its progress. A run that outgrows the
 * model's context window fails with the provider's error.
 */
export type SearchMode = 'search' | 'research'

/**
 * `session` is null when `saved` is false: the provider replied but the write
 * failed, so the answer is shown and the conversation is not resumable.
 */
export type ChatTurnResult = {
  session: ChatSession | null
  message: ChatMessageRecord
  saved: boolean
  detail?: string
}

type RawTurnResponse = {
  session?: ChatSession | null
  message?: ChatMessageRecord
  saved?: boolean
  detail?: string
}

export async function chatWithDocument(input: {
  documentId: string
  sessionId?: string
  content: string
  runId: string
  /**
   * Lets this turn reach the public web. Per turn rather than stored with the
   * conversation: nothing in the transcript depends on it, and a metered tool
   * is better defaulted off on every reload. Ignored unless a provider is
   * bound -- see `AppMeta.webSearch`.
   */
  web?: boolean
  /**
   * The provider and model to open the conversation on. Read by the server
   * only when there is no session id yet: a conversation keeps the binding its
   * transcript was produced with.
   */
  binding?: ProviderBinding
}): Promise<ChatTurnResult> {
  const data = await apiFetch<RawTurnResponse>(
    `/api/app/documents/${encodeURIComponent(input.documentId)}/chat`,
    {
      method: 'POST',
      body: {
        session_id: input.sessionId ?? '',
        content: input.content,
        run_id: input.runId,
        web: input.web === true,
        ...bindingBody(input.binding),
      },
      fallbackError: 'Failed to get AI response',
    },
  )
  if (!data.message) {
    throw new Error('AI response was empty')
  }
  return {
    session: data.session ?? null,
    message: data.message,
    saved: data.saved ?? false,
    detail: data.detail,
  }
}

export type ResearchStepKind =
  | 'search'
  | 'read'
  | 'survey'
  | 'count'
  | 'web_search'
  | 'web_fetch'
  | 'answer'

export type ResearchEvent =
  // First event of every run: the conversation it writes into, which exists
  // before the run does. It is what makes a turn whose stream died recoverable
  // (see `waitForStoredTurn`) and how the page learns a new chat's id.
  | { type: 'session'; session: ChatSession }
  | {
      type: 'step'
      kind: ResearchStepKind
      /** `progress` is a survey's running count; only surveys emit it. */
      status: 'start' | 'progress' | 'done'
      query?: string
      titles?: string[]
      count?: number
      /** Documents finished so far, out of `count`, on a progress event. */
      done?: number
      /** A read the helper model summarised instead of passing text through. */
      distilled?: boolean
    }
  | { type: 'delta'; content: string }
  // How wide the research conversation has grown, emitted after every
  // completion the main thread makes. Helper models are a separate
  // conversation and are not counted.
  | {
      type: 'usage'
      prompt_tokens: number
      /** The model's limit, absent when no catalogue knows it. */
      context_window?: number
      /** True when the provider reported nothing and this was estimated. */
      estimated?: boolean
    }
  | { type: 'documents'; documents?: SearchDocumentHit[] }
  | { type: 'message'; content: string; incomplete?: boolean }
  // Closes a successful run with the stored turn; the answer itself already
  // arrived above.
  | {
      type: 'saved'
      session: ChatSession | null
      message: ChatMessageRecord
      documents?: SearchDocumentHit[]
      saved: boolean
      detail?: string
    }
  | { type: 'error'; message: string }
  | { type: 'done' }

/**
 * A research answer arrives twice: as `delta` events for a live preview, then
 * as one `message` event with the citation-checked text, whose `incomplete`
 * says whether the generation was cut short.
 *
 * Plain search emits only `documents`, `message` and `saved`, but streams
 * regardless: a POST that writes nothing for minutes is what a reverse proxy
 * cannot tell from a hung backend.
 *
 * `runId` is what makes a run cancellable, since the server does not stop when
 * this connection closes -- see `cancelSearchRun`.
 */
export async function searchStream(
  input: {
    sessionId?: string
    content: string
    mode: SearchMode
    runId: string
    /** See `chatWithDocument`. Research mode only; search mode ignores it. */
    web?: boolean
    /** Read only when there is no session id yet; see `chatWithDocument`. */
    binding?: ProviderBinding
    /**
     * Finishes a research turn whose run did not: no new question, the stored
     * conversation is replayed and the loop re-entered where it stopped.
     */
    resume?: boolean
  },
  onEvent: (event: ResearchEvent) => void,
  signal?: AbortSignal,
) {
  await apiStream<ResearchEvent>('/api/app/search/stream', {
    body: {
      session_id: input.sessionId ?? '',
      content: input.content,
      mode: input.mode,
      run_id: input.runId,
      web: input.web === true,
      resume: input.resume === true,
      ...bindingBody(input.binding),
    },
    onEvent,
    signal,
    fallbackError:
      input.mode === 'research' ? 'Failed to research your archive' : 'Failed to search your archive',
  })
}

/**
 * Abandoning the stream is deliberately not enough: the server keeps a run
 * alive through a dropped connection, so a cancel needs a request of its own.
 * Best-effort, since a finished run has nothing to stop.
 */
export async function cancelSearchRun(
  target: { runId: string } | { sessionId: string },
): Promise<void> {
  try {
    await apiFetch('/api/app/search/cancel', {
      method: 'POST',
      body: 'runId' in target ? { run_id: target.runId } : { session_id: target.sessionId },
      fallbackError: 'Failed to cancel the run',
    })
  } catch {
    // Nothing to tell the user: they have already moved on.
  }
}
