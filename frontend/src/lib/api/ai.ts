import { apiFetch } from '../apiClient'
import type { ChatMessageRecord, ChatSession, SearchDocumentHit } from './chats'

export type { SearchDocumentHit } from './chats'

/** The minimal message shape the transcript renderer needs. */
export type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
}

export type SearchMode = 'shallow' | 'deep'

/**
 * A turn the server answered.
 *
 * `session` is null when `saved` is false: the provider replied but the write
 * failed, so the answer is shown and the conversation is not resumable.
 */
export type ChatTurnResult = {
  session: ChatSession | null
  message: ChatMessageRecord
  saved: boolean
}

export type DeepSearchResult = ChatTurnResult & {
  documents: SearchDocumentHit[]
}

type RawTurnResponse = {
  session?: ChatSession | null
  message?: ChatMessageRecord
  documents?: SearchDocumentHit[]
  saved?: boolean
}

export async function deepSearch(input: {
  sessionId?: string
  content: string
  mode: SearchMode
}): Promise<DeepSearchResult> {
  const data = await apiFetch<RawTurnResponse>('/api/app/search', {
    method: 'POST',
    body: { session_id: input.sessionId ?? '', content: input.content, mode: input.mode },
    fallbackError: 'Failed to run deep search',
  })
  if (!data.message) {
    throw new Error('AI response was empty')
  }
  return {
    session: data.session ?? null,
    message: data.message,
    documents: data.documents ?? [],
    saved: data.saved ?? false,
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
  }
}
