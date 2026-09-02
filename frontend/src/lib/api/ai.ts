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

export type DeepSearchResult = ChatTurnResult & {
  documents: SearchDocumentHit[]
  /** The generation was cut short; the text is real but not the whole answer. */
  incomplete?: boolean
}

type RawTurnResponse = {
  session?: ChatSession | null
  message?: ChatMessageRecord
  documents?: SearchDocumentHit[]
  saved?: boolean
  detail?: string
  incomplete?: boolean
}

export async function deepSearch(input: {
  sessionId?: string
  content: string
  mode: SearchMode
}): Promise<DeepSearchResult> {
  const data = await apiFetch<RawTurnResponse>('/api/app/search', {
    method: 'POST',
    body: { session_id: input.sessionId ?? '', content: input.content, mode: input.mode },
    fallbackError: 'Failed to run search',
  })
  if (!data.message) {
    throw new Error('AI response was empty')
  }
  return {
    session: data.session ?? null,
    message: data.message,
    documents: data.documents ?? [],
    saved: data.saved ?? false,
    detail: data.detail,
    incomplete: data.incomplete,
  }
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
 * Runs a research turn, reporting each step as it happens. The answer arrives
 * twice: as `delta` events for a live preview, then as one `message` event with
 * the authoritative, citation-checked text. That event's `incomplete` says
 * whether the generation was cut short — the text is kept either way, but a
 * partial answer must not be shown as a finished one.
 */
export async function researchStream(
  input: { sessionId?: string; content: string },
  onEvent: (event: ResearchEvent) => void,
  signal?: AbortSignal,
) {
  await apiStream<ResearchEvent>('/api/app/search/stream', {
    body: {
      session_id: input.sessionId ?? '',
      content: input.content,
      mode: 'research' satisfies SearchMode,
    },
    onEvent,
    signal,
    fallbackError: 'Failed to research your archive',
  })
}
