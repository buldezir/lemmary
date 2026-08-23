import { apiFetch } from '../apiClient'

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

export type SearchMode = 'shallow' | 'deep'

export async function deepSearch(messages: ChatMessage[], mode: SearchMode = 'shallow') {
  const data = await apiFetch<{ message?: ChatMessage; documents?: SearchDocumentHit[] }>(
    '/api/app/search',
    {
      method: 'POST',
      body: { messages, mode },
      fallbackError: 'Failed to run deep search',
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
