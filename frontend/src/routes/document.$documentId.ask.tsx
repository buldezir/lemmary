import { type SubmitEvent, useEffect, useRef, useState } from 'react'
import { Link, useParams } from '@tanstack/react-router'
import { MarkdownContent } from '../components/MarkdownContent'
import { Button } from '../components/ui'
import { pb } from '../lib/pb'
import { ensureAuth } from '../lib/auth'
import { chatWithDocument, type ChatMessage } from '../lib/api/ai'
import type { DocumentRecord } from '../lib/api/documents'
import { useAsync } from '../hooks/useAsync'

export function DocumentAskPage() {
  const { documentId } = useParams({ from: '/document/$documentId/ask' })
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)

  const {
    data: document,
    loading,
    error: loadError,
  } = useAsync(async () => {
    await ensureAuth()
    return pb.collection('documents').getOne<DocumentRecord>(documentId)
  }, [documentId])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, sending])

  const hasOcrText = Boolean(document?.ocr_text?.trim())

  async function send() {
    const text = input.trim()
    if (!text || sending || !hasOcrText) {
      return
    }

    const userMessage: ChatMessage = { role: 'user', content: text }
    const nextMessages = [...messages, userMessage]

    try {
      setSending(true)
      setInput('')
      setError('')
      setMessages(nextMessages)

      const reply = await chatWithDocument(documentId, nextMessages)
      setMessages([...nextMessages, reply])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to get AI response')
      setMessages(messages)
      setInput(text)
    } finally {
      setSending(false)
    }
  }

  function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    void send()
  }

  if (loading) {
    return <p className="text-sm text-stone-500">Loading...</p>
  }

  if (!document) {
    return (
      <section className="flex flex-col gap-3">
        <p className="text-sm text-red-600">{loadError || 'Document not found.'}</p>
        <Link to="/" className="text-sm font-medium text-gray-900 underline">
          Back to documents
        </Link>
      </section>
    )
  }

  return (
    <section className="flex flex-col gap-4">
      <div>
        <Link
          to="/document/$documentId"
          params={{ documentId }}
          className="text-sm text-stone-500 hover:text-stone-950"
        >
          &larr; Back to document
        </Link>
        <h2 className="mt-1 text-xl font-semibold text-stone-950">
          Ask AI: {document.title || 'Untitled document'}
        </h2>
        <p className="text-sm text-stone-500">
          Questions are answered using the document&apos;s OCR text as context.
        </p>
      </div>

      {!hasOcrText ? (
        <div className="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800">
          This document has no OCR text yet. Run full processing before asking questions.
        </div>
      ) : (
        <div className="flex min-h-128 flex-col overflow-hidden rounded-lg border border-stone-200 bg-stone-50">
          <div className="flex-1 space-y-4 overflow-y-auto p-4">
            {messages.length === 0 && (
              <p className="text-sm text-stone-400">
                Ask a question about this document, for example: &quot;What is the total amount?&quot;
              </p>
            )}
            {messages.map((message, index) => (
              <div
                key={index}
                className={`flex ${message.role === 'user' ? 'justify-end' : 'justify-start'}`}
              >
                <div
                  className={`max-w-[85%] rounded-lg px-4 py-2.5 text-sm leading-relaxed ${
                    message.role === 'user'
                      ? 'whitespace-pre-wrap bg-gray-900 text-white'
                      : 'border border-stone-200 bg-stone-100 text-stone-950'
                  }`}
                >
                  {message.role === 'user' ? (
                    message.content
                  ) : (
                    <MarkdownContent content={message.content} />
                  )}
                </div>
              </div>
            ))}
            {sending && (
              <div className="flex justify-start">
                <div className="rounded-lg border border-stone-200 bg-stone-100 px-4 py-2.5 text-sm text-stone-500">
                  Thinking...
                </div>
              </div>
            )}
            <div ref={messagesEndRef} />
          </div>

          <form onSubmit={onSubmit} className="border-t border-stone-200 bg-stone-100/60 p-4">
            <div className="flex items-end gap-3">
              <textarea
                rows={2}
                value={input}
                onChange={(event) => setInput(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey) {
                    event.preventDefault()
                    void send()
                  }
                }}
                autoFocus
                disabled={sending}
                placeholder="Ask a question about this document..."
                className="min-h-12 flex-1 resize-y rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm text-stone-950 outline-none placeholder:text-stone-400 focus:border-gray-900 focus:ring-1 focus:ring-gray-900 disabled:cursor-not-allowed disabled:opacity-50"
              />
              <Button type="submit" disabled={sending || !input.trim()}>
                {sending ? 'Sending...' : 'Send'}
              </Button>
            </div>
            {error && <p className="mt-2 text-sm text-red-600">{error}</p>}
          </form>
        </div>
      )}
    </section>
  )
}
