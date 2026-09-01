import { type ReactNode, useEffect, useRef } from 'react'
import { MarkdownContent } from './MarkdownContent'
import type { ChatTurn } from '../lib/api/chats'

type ChatTranscriptProps = {
  turns: ChatTurn[]
  /** Shown when the conversation is empty. */
  emptyHint: string
  loading?: boolean
  sending: boolean
  /** Text of the placeholder bubble while a reply is in flight. */
  sendingLabel: string
  /** Rendered under an assistant bubble — the search hit grid. */
  renderExtra?: (turn: ChatTurn) => ReactNode
  /** Rendered above a bubble — the research steps that produced it. */
  renderBefore?: (turn: ChatTurn) => ReactNode
  /**
   * Replaces the placeholder bubble while a reply is in flight, for a send that
   * has something better to show than one label: research reports each step as
   * it happens and streams the answer.
   */
  renderSending?: () => ReactNode
}

export function ChatTranscript({
  turns,
  emptyHint,
  loading = false,
  sending,
  sendingLabel,
  renderExtra,
  renderBefore,
  renderSending,
}: ChatTranscriptProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  // Opening a saved conversation should land at the bottom, not animate its
  // whole length; only turns arriving afterwards are worth a smooth scroll.
  const settledRef = useRef(false)

  useEffect(() => {
    const container = scrollRef.current
    if (!container) {
      return
    }
    if (!settledRef.current) {
      container.scrollTop = container.scrollHeight
      settledRef.current = turns.length > 0
      return
    }
    container.scrollTo({ top: container.scrollHeight, behavior: 'smooth' })
  }, [turns, sending])

  return (
    <div
      ref={scrollRef}
      role="log"
      aria-label="Chat transcript"
      className="flex-1 space-y-4 overflow-y-auto p-4"
    >
      {loading && <p className="text-sm text-ink-soft">Loading chat...</p>}
      {!loading && turns.length === 0 && <p className="text-sm text-ink-faint">{emptyHint}</p>}
      {turns.map((turn) => (
        <div key={turn.id} className="space-y-3">
          {renderBefore?.(turn)}
          <div className={`flex ${turn.role === 'user' ? 'justify-end' : 'justify-start'}`}>
            <div
              className={`max-w-[85%] rounded-none px-4 py-2.5 text-sm leading-relaxed ${
                turn.role === 'user'
                  ? 'whitespace-pre-wrap bg-ink text-paper'
                  : 'border border-line bg-paper text-ink'
              }`}
            >
              {turn.role === 'user' ? turn.content : <MarkdownContent content={turn.content} />}
            </div>
          </div>
          {renderExtra?.(turn)}
        </div>
      ))}
      {sending &&
        (renderSending?.() ?? (
          <div className="flex justify-start">
            <div className="rounded-none border border-line bg-paper px-4 py-2.5 text-sm text-ink-soft">
              {sendingLabel}
            </div>
          </div>
        ))}
    </div>
  )
}
