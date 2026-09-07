import { apiFetch, apiStream } from '../apiClient'
import type { ChatMessageRecord, ChatSession, SearchDocumentHit } from './chats'

export type { SearchDocumentHit } from './chats'

/** The minimal message shape the transcript renderer needs. */
export type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
}

/**
 * `search` finds documents and lists them as cards. `research` reads the
 * documents it finds and writes a cited answer — it can take a while and
 * streams its progress. A run that outgrows the model's context window fails
 * with the provider's error.
 */
export type SearchMode = 'search' | 'research'

/**
 * A turn the server answered.
 *
 * `session` is null when `saved` is false: the provider replied but the write
 * failed, so the answer is shown and the conversation is not resumable.
 * `detail` then says why.
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
}): Promise<ChatTurnResult> {
  const data = await apiFetch<RawTurnResponse>(
    `/api/app/documents/${encodeURIComponent(input.documentId)}/chat`,
    {
      method: 'POST',
      body: { session_id: input.sessionId ?? '', content: input.content },
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

export type ResearchStepKind = 'search' | 'read' | 'survey' | 'count' | 'answer'

export type ResearchEvent =
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
  | { type: 'documents'; documents?: SearchDocumentHit[] }
  | { type: 'message'; content: string; incomplete?: boolean }
  // Closes a successful run with the stored turn, which is what makes the
  // conversation resumable — the answer itself already arrived above.
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
 * Runs a search turn as a stream.
 *
 * Research reports each step as it happens, and its answer arrives twice: as
 * `delta` events for a live preview, then as one `message` event with the
 * authoritative, citation-checked text. That event's `incomplete` says whether
 * the generation was cut short — the text is kept either way, but a partial
 * answer must not be shown as a finished one.
 *
 * Plain search emits none of those, only `documents`, `message` and `saved`.
 * It streams regardless, because the alternative is a POST that writes nothing
 * for however long the model takes, and a reverse proxy cannot tell that apart
 * from a backend that has hung.
 *
 * `runId` is what makes the run cancellable: the server no longer stops when
 * this connection closes, so cancelling has to be said out loud with
 * `cancelSearchRun`.
 */
export async function searchStream(
  input: { sessionId?: string; content: string; mode: SearchMode; runId: string },
  onEvent: (event: ResearchEvent) => void,
  signal?: AbortSignal,
) {
  await apiStream<ResearchEvent>('/api/app/search/stream', {
    body: {
      session_id: input.sessionId ?? '',
      content: input.content,
      mode: input.mode,
      run_id: input.runId,
    },
    onEvent,
    signal,
    fallbackError:
      input.mode === 'research' ? 'Failed to research your archive' : 'Failed to search your archive',
  })
}

/**
 * Stops a run started with `searchStream`.
 *
 * Abandoning the stream is not enough on its own and is no longer meant to be:
 * the server keeps a run alive through a dropped connection so a network blip
 * cannot destroy an answer it has already paid for, which means a deliberate
 * cancel needs a request of its own. Best-effort — a run that already finished
 * has nothing to stop, and a failure here must not surface over a turn the user
 * has abandoned anyway.
 */
export async function cancelSearchRun(runId: string): Promise<void> {
  try {
    await apiFetch('/api/app/search/cancel', {
      method: 'POST',
      body: { run_id: runId },
      fallbackError: 'Failed to cancel the run',
    })
  } catch {
    // Nothing to tell the user: they have already moved on.
  }
}
