import { apiFetch, apiStream } from '../apiClient'

export type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
}

export async function chatWithDocument(documentId: string, messages: ChatMessage[]) {
  const data = await apiFetch<{ message?: ChatMessage }>(`/api/app/documents/${documentId}/chat`, {
    method: 'POST',
    body: { messages },
    fallbackError: 'Failed to get AI response',
  })
  if (!data.message) {
    throw new Error('AI response was empty')
  }
  return data.message
}

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

/**
 * `search` finds documents and lists them as cards. `research` reads the
 * documents it finds and writes a cited answer — the only bound on it is the
 * model's context window, so it can take a while and streams its progress.
 */
export type SearchMode = 'search' | 'research'

export async function deepSearch(messages: ChatMessage[], mode: SearchMode = 'search') {
  const data = await apiFetch<{ message?: ChatMessage; documents?: SearchDocumentHit[] }>(
    '/api/app/search',
    {
      method: 'POST',
      body: { messages, mode },
      fallbackError: 'Failed to run search',
    },
  )
  if (!data.message) {
    throw new Error('AI response was empty')
  }
  return {
    message: data.message,
    documents: data.documents ?? [],
  }
}

export type ResearchStepKind = 'search' | 'read' | 'answer'

export type ResearchEvent =
  | {
      type: 'step'
      kind: ResearchStepKind
      status: 'start' | 'done'
      query?: string
      titles?: string[]
      count?: number
      context_left_pct?: number
    }
  | { type: 'delta'; content: string }
  | { type: 'documents'; documents?: SearchDocumentHit[] }
  | { type: 'message'; content: string }
  | { type: 'error'; message: string }
  | { type: 'done' }

/**
 * Runs a research turn, reporting each step as it happens. The answer arrives
 * twice: as `delta` events for a live preview, then as one `message` event with
 * the authoritative text — citation-checked, and complete even when the
 * upstream stream was cut short.
 */
export async function researchStream(
  messages: ChatMessage[],
  onEvent: (event: ResearchEvent) => void,
  signal?: AbortSignal,
) {
  await apiStream<ResearchEvent>('/api/app/search/stream', {
    body: { messages, mode: 'research' satisfies SearchMode },
    onEvent,
    signal,
    fallbackError: 'Failed to research your archive',
  })
}
