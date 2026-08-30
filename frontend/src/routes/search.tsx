import { type SubmitEvent, useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { MarkdownContent } from '../components/MarkdownContent'
import { Button } from '../components/ui'
import {
  deepSearch,
  researchStream,
  type ChatMessage,
  type ResearchEvent,
  type SearchDocumentHit,
  type SearchMode,
} from '../lib/api/ai'

type ResearchStep = {
  kind: 'search' | 'read' | 'answer'
  label: string
  done: boolean
}

type SearchTurn = {
  message: ChatMessage
  /** Cards, for search mode. Research answers link to documents inline instead. */
  documents?: SearchDocumentHit[]
  steps?: ResearchStep[]
}

const modes: { value: SearchMode; label: string; hint: string }[] = [
  { value: 'search', label: 'Search', hint: 'Find documents and list them.' },
  {
    value: 'research',
    label: 'Research',
    hint: 'Read the documents and answer, with citations.',
  },
]

const placeholders: Record<SearchMode, string> = {
  search: 'Describe what you are looking for...',
  research: 'Ask a question about your documents...',
}

const examples: Record<SearchMode, string> = {
  search: 'plumber invoice from last summer about the leak',
  research: 'how much did I spend on the car in 2024?',
}

export function SearchPage() {
  const [turns, setTurns] = useState<SearchTurn[]>([])
  const [input, setInput] = useState('')
  const [mode, setMode] = useState<SearchMode>('search')
  const [sending, setSending] = useState(false)
  const [steps, setSteps] = useState<ResearchStep[]>([])
  const [draft, setDraft] = useState('')
  const [error, setError] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [turns, sending, steps, draft])

  async function send() {
    const text = input.trim()
    if (!text || sending) {
      return
    }

    const userMessage: ChatMessage = { role: 'user', content: text }
    const history: ChatMessage[] = [...turns.map((turn) => turn.message), userMessage]

    try {
      setSending(true)
      setInput('')
      setError('')
      setSteps([])
      setDraft('')
      setTurns((current) => [...current, { message: userMessage }])

      if (mode === 'research') {
        await runResearch(history)
      } else {
        const result = await deepSearch(history, 'search')
        setTurns((current) => [
          ...current,
          { message: result.message, documents: result.documents },
        ])
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to run search')
      setTurns((current) => current.slice(0, -1))
      setInput(text)
    } finally {
      setSending(false)
      setSteps([])
      setDraft('')
    }
  }

  async function runResearch(history: ChatMessage[]) {
    // Collected outside React state as well: the final turn is assembled from
    // these, and state updates are not readable synchronously.
    const collected: ResearchStep[] = []
    let answer = ''
    let streamError = ''

    await researchStream(history, (event) => {
      switch (event.type) {
        case 'step': {
          applyStep(collected, event)
          setSteps([...collected])
          break
        }
        case 'delta':
          answer += event.content
          setDraft(answer)
          break
        case 'message':
          answer = event.content
          setDraft(answer)
          break
        case 'error':
          streamError = event.message
          break
        default:
          break
      }
    })

    if (streamError) {
      throw new Error(streamError)
    }
    setTurns((current) => [
      ...current,
      {
        message: { role: 'assistant', content: answer },
        steps: collected.map((step) => ({ ...step, done: true })),
      },
    ])
  }

  function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    void send()
  }

  const active = modes.find((item) => item.value === mode) ?? modes[0]

  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">Deep Search</h2>
          <p className="text-sm text-ink-soft">{active.hint}</p>
        </div>
        <div
          role="radiogroup"
          aria-label="Search mode"
          className="flex rounded-xs border border-line bg-surface p-1"
        >
          {modes.map((item) => (
            <button
              key={item.value}
              type="button"
              role="radio"
              aria-checked={mode === item.value}
              disabled={sending}
              onClick={() => setMode(item.value)}
              className={`px-3 py-1.5 text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
                mode === item.value
                  ? 'bg-ink text-paper'
                  : 'text-ink-muted hover:text-ink'
              }`}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>

      <div className="flex min-h-128 flex-col overflow-hidden rounded-none border border-line bg-surface">
        <div className="flex-1 space-y-4 overflow-y-auto p-4">
          {turns.length === 0 && (
            <p className="text-sm text-ink-faint">
              Try something like: &quot;{examples[mode]}&quot;
            </p>
          )}
          {turns.map((turn, index) => (
            <div key={index} className="space-y-3">
              {turn.steps && turn.steps.length > 0 && <StepList steps={turn.steps} collapsed />}
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
          {sending && mode === 'research' && (
            <div className="space-y-3">
              <StepList steps={steps} />
              {draft && (
                <div className="flex justify-start">
                  <div className="max-w-[85%] rounded-none border border-line bg-paper px-4 py-2.5 text-sm leading-relaxed text-ink">
                    <MarkdownContent content={draft} />
                  </div>
                </div>
              )}
            </div>
          )}
          {sending && mode !== 'research' && (
            <div className="flex justify-start">
              <div className="rounded-none border border-line bg-paper px-4 py-2.5 text-sm text-ink-soft">
                Searching...
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
              placeholder={placeholders[mode]}
              className="min-h-12 flex-1 resize-y rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood disabled:cursor-not-allowed disabled:opacity-50"
            />
            <Button type="submit" disabled={sending || !input.trim()}>
              {sending ? (mode === 'research' ? 'Researching...' : 'Searching...') : active.label}
            </Button>
          </div>
          {error && <p className="mt-2 text-sm text-madder">{error}</p>}
        </form>
      </div>
    </section>
  )
}

/**
 * Folds one event into the visible step list: a "start" appends a pending step,
 * the matching "done" completes it in place rather than adding a second line.
 */
function applyStep(steps: ResearchStep[], event: Extract<ResearchEvent, { type: 'step' }>) {
  if (event.status === 'start') {
    steps.push({ kind: event.kind, label: startLabel(event), done: false })
    return
  }
  const pending = [...steps].reverse().find((step) => step.kind === event.kind && !step.done)
  if (!pending) {
    steps.push({ kind: event.kind, label: doneLabel(event), done: true })
    return
  }
  pending.label = doneLabel(event, pending.label)
  pending.done = true
}

function startLabel(event: Extract<ResearchEvent, { type: 'step' }>) {
  switch (event.kind) {
    case 'search':
      return event.query ? `Searching “${event.query}”` : 'Searching'
    case 'read':
      return `Reading ${event.count ?? 0} document${event.count === 1 ? '' : 's'}`
    default:
      return 'Writing answer'
  }
}

function doneLabel(event: Extract<ResearchEvent, { type: 'step' }>, fallback?: string) {
  switch (event.kind) {
    case 'search': {
      const found = `${event.count ?? 0} document${event.count === 1 ? '' : 's'} found`
      return event.query ? `“${event.query}” — ${found}` : found
    }
    case 'read': {
      const titles = event.titles ?? []
      const shown = titles.slice(0, 3).join(', ')
      const rest = titles.length > 3 ? `, and ${titles.length - 3} more` : ''
      return titles.length > 0 ? `Read ${shown}${rest}` : (fallback ?? 'Read documents')
    }
    default:
      return 'Answer written'
  }
}

function StepList({ steps, collapsed = false }: { steps: ResearchStep[]; collapsed?: boolean }) {
  if (steps.length === 0) {
    return (
      <p className="text-xs text-ink-faint">
        <span className="animate-pulse">Researching your archive…</span>
      </p>
    )
  }

  const list = (
    <ol className="space-y-1">
      {steps.map((step, index) => (
        <li key={index} className="flex items-baseline gap-2 text-xs text-ink-muted">
          <span
            aria-hidden
            className={`font-mono ${step.done ? 'text-ink-faint' : 'animate-pulse text-oxblood'}`}
          >
            {step.done ? '✓' : '·'}
          </span>
          <span className={step.done ? '' : 'text-ink'}>{step.label}</span>
        </li>
      ))}
    </ol>
  )

  if (!collapsed) {
    return <div className="border-l-2 border-line pl-3">{list}</div>
  }
  return (
    <details className="border-l-2 border-line pl-3">
      <summary className="cursor-pointer text-xs text-ink-faint">
        {steps.length} research step{steps.length === 1 ? '' : 's'}
      </summary>
      <div className="mt-1">{list}</div>
    </details>
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
