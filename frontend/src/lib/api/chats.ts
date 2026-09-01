import { apiFetch } from '../apiClient'

export type ChatSessionKind = 'search' | 'document'
export type ChatRole = 'user' | 'assistant'

/** A document the search agent cited, captured with the answer that cited it. */
export type SearchDocumentHit = {
  id: string
  title: string
  document_date?: string
  summary?: string
  ocr_snippet?: string
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
