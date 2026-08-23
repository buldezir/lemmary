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
    return <p className="text-sm text-ink-soft">Loading...</p>
  }

  if (!document) {
    return (
      <section className="flex flex-col gap-3">
        <p className="text-sm text-madder">{loadError || 'Document not found.'}</p>
        <Link to="/" className="text-sm font-medium text-oxblood underline">
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
          className="text-sm text-ink-soft hover:text-oxblood"
        >
          &larr; Back to document
        </Link>
        <h2 className="mt-1 font-display text-2xl font-semibold tracking-tight text-ink">
          Ask AI: {document.title || 'Untitled document'}
        </h2>
        <p className="text-sm text-ink-soft">
          Questions are answered using the document&apos;s OCR text as context.
        </p>
      </div>

      {!hasOcrText ? (
        <div className="rounded-none border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800">
          This document has no OCR text yet. Run full processing before asking questions.
        </div>
      ) : (
        <div className="flex min-h-128 flex-col overflow-hidden rounded-none border border-line bg-surface">
          <div className="flex-1 space-y-4 overflow-y-auto p-4">
            {messages.length === 0 && (
              <p className="text-sm text-ink-faint">
                Ask a question about this document, for example: &quot;What is the total amount?&quot;
              </p>
            )}
            {messages.map((message, index) => (
              <div
                key={index}
                className={`flex ${message.role === 'user' ? 'justify-end' : 'justify-start'}`}
              >
                <div
                  className={`max-w-[85%] rounded-none px-4 py-2.5 text-sm leading-relaxed ${
                    message.role === 'user'
                      ? 'whitespace-pre-wrap bg-ink text-paper'
                      : 'border border-line bg-paper text-ink'
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
                <div className="rounded-none border border-line bg-paper px-4 py-2.5 text-sm text-ink-soft">
                  Thinking...
                </div>
              </div>
            )}
            <div ref={messagesEndRef} />
          </div>

          <form onSubmit={onSubmit} className="border-t border-line bg-paper/70 p-4">
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
                className="min-h-12 flex-1 resize-y rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood disabled:cursor-not-allowed disabled:opacity-50"
              />
              <Button type="submit" disabled={sending || !input.trim()}>
                {sending ? 'Sending...' : 'Send'}
              </Button>
            </div>
            {error && <p className="mt-2 text-sm text-madder">{error}</p>}
          </form>
        </div>
      )}
    </section>
  )
}
