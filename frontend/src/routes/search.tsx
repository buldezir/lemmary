import { type SubmitEvent, useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { MarkdownContent } from '../components/MarkdownContent'
import { Button } from '../components/ui'
import {
  deepSearch,
  type ChatMessage,
  type SearchDocumentHit,
  type SearchMode,
} from '../lib/api/ai'

type SearchTurn = {
  message: ChatMessage
  documents?: SearchDocumentHit[]
}

export function SearchPage() {
  const [turns, setTurns] = useState<SearchTurn[]>([])
  const [input, setInput] = useState('')
  const [deepMode, setDeepMode] = useState(false)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [turns, sending])

  async function send() {
    const text = input.trim()
    if (!text || sending) {
      return
    }

    const userMessage: ChatMessage = { role: 'user', content: text }
    const history: ChatMessage[] = [...turns.map((turn) => turn.message), userMessage]
    const mode: SearchMode = deepMode ? 'deep' : 'shallow'

    try {
      setSending(true)
      setInput('')
      setError('')
      setTurns((current) => [...current, { message: userMessage }])

      const result = await deepSearch(history, mode)
      setTurns((current) => [...current, { message: result.message, documents: result.documents }])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to run deep search')
      setTurns((current) => current.slice(0, -1))
      setInput(text)
    } finally {
      setSending(false)
    }
  }

  function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    void send()
  }

  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">Deep Search</h2>
          <p className="text-sm text-ink-soft">
            Ask in natural language. The AI expands keywords across your archive languages and
            searches document metadata and OCR text.
          </p>
        </div>
        <label className="flex cursor-pointer items-center gap-2 rounded-xs border border-line bg-surface px-3 py-2 text-sm text-ink-muted">
          <input
            type="checkbox"
            checked={deepMode}
            onChange={(event) => setDeepMode(event.target.checked)}
            className="h-4 w-4 rounded border-line-strong text-oxblood focus:ring-oxblood"
          />
          <span>
            Deep mode
            <span className="ml-1 text-ink-faint">(multi-step refine)</span>
          </span>
        </label>
      </div>

      <div className="flex min-h-128 flex-col overflow-hidden rounded-none border border-line bg-surface">
        <div className="flex-1 space-y-4 overflow-y-auto p-4">
          {turns.length === 0 && (
            <p className="text-sm text-ink-faint">
              Try something like: &quot;plumber invoice from last summer about the leak&quot;
            </p>
          )}
          {turns.map((turn, index) => (
            <div key={index} className="space-y-3">
              <div
                className={`flex ${turn.message.role === 'user' ? 'justify-end' : 'justify-start'}`}
              >
                <div
                  className={`max-w-[85%] rounded-none px-4 py-2.5 text-sm leading-relaxed ${
                    turn.message.role === 'user'
                      ? 'whitespace-pre-wrap bg-ink text-paper'
                      : 'border border-line bg-paper text-ink'
                  }`}
                >
                  {turn.message.role === 'user' ? (
                    turn.message.content
                  ) : (
                    <MarkdownContent content={turn.message.content} />
                  )}
                </div>
              </div>
              {turn.documents && turn.documents.length > 0 && (
                <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                  {turn.documents.map((doc) => (
                    <SearchHitCard key={doc.id} document={doc} />
                  ))}
                </div>
              )}
            </div>
          ))}
          {sending && (
            <div className="flex justify-start">
              <div className="rounded-none border border-line bg-paper px-4 py-2.5 text-sm text-ink-soft">
                {deepMode ? 'Searching deeply...' : 'Searching...'}
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
              placeholder="Describe what you are looking for..."
              className="min-h-12 flex-1 resize-y rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood disabled:cursor-not-allowed disabled:opacity-50"
            />
            <Button type="submit" disabled={sending || !input.trim()}>
              {sending ? 'Searching...' : 'Search'}
            </Button>
          </div>
          {error && <p className="mt-2 text-sm text-madder">{error}</p>}
        </form>
      </div>
    </section>
  )
}

function SearchHitCard({ document }: { document: SearchDocumentHit }) {
  const meta = [document.document_type, document.correspondent].filter(Boolean).join(' · ')

  return (
    <Link
      to="/document/$documentId"
      params={{ documentId: document.id }}
      className="flex flex-col gap-1.5 rounded-none border border-line bg-bright p-3 transition-colors hover:border-ink/40 hover:shadow-sm"
    >
      <div className="flex items-start justify-between gap-2">
        <h3 className="font-display text-base font-semibold leading-snug text-ink">{document.title}</h3>
        {document.document_date && (
          <span className="shrink-0 font-mono text-xs tabular-nums text-ink-soft">{document.document_date}</span>
        )}
      </div>
      {meta && <p className="text-xs text-ink-soft">{meta}</p>}
      <p className="line-clamp-3 text-xs text-ink-muted">
        {document.ocr_snippet || document.summary || 'No preview.'}
      </p>
      {document.tags && document.tags.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {document.tags.slice(0, 4).map((tag) => (
            <span key={tag} className="border border-line px-1.5 py-0.5 text-[11px] text-ink-muted">
              {tag}
            </span>
          ))}
        </div>
      )}
    </Link>
  )
}
