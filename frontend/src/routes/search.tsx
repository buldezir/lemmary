import { useCallback, useState } from 'react'
import { Link, useMatchRoute, useNavigate } from '@tanstack/react-router'
import { Button } from '../components/ui'
import { ChatPanel } from '../components/ChatPanel'
import { ChatTranscript } from '../components/ChatTranscript'
import { ChatComposer } from '../components/ChatComposer'
import { ChatSessionList } from '../components/ChatSessionList'
import { useAsync } from '../hooks/useAsync'
import { useChatSession } from '../hooks/useChatSession'
import { deepSearch } from '../lib/api/ai'
import {
  deleteChatSession,
  getChatSession,
  listChatSessions,
  mergeChatSession,
  renameChatSession,
  type ChatSession,
  type ChatTurn,
  type SearchDocumentHit,
} from '../lib/api/chats'

export function SearchPage() {
  const navigate = useNavigate()
  // The session id lives on a child route, and a child's params are invisible
  // to useParams from here — the closest match is /search, which has none.
  // matchRoute also hands back a fresh object each render, so the id is
  // destructured out before anything depends on it.
  const matchRoute = useMatchRoute()
  const sessionMatch = matchRoute({ to: '/search/$sessionId' })
  const sessionId = sessionMatch ? (sessionMatch.sessionId as string) : undefined

  const [deepMode, setDeepMode] = useState(false)
  const [railOpen, setRailOpen] = useState(false)
  const [justSettled, setJustSettled] = useState<ChatSession | null>(null)
  const [railBusy, setRailBusy] = useState(false)
  const [railError, setRailError] = useState('')

  const sessions = useAsync(() => listChatSessions({ kind: 'search' }), [])

  const onSessionSettled = useCallback(
    (session: ChatSession, created: boolean) => {
      // Merged in straight away so the row is there with the transcript, not a
      // round trip later.
      setJustSettled(session)
      if (created) {
        // replace: Back should not land on the now-orphaned empty /search.
        void navigate({
          to: '/search/$sessionId',
          params: { sessionId: session.id },
          replace: true,
        })
      }
      // After every turn, not only the first: last_message_at moved and the row
      // has to move with it. reload() refreshes without a loading flash.
      void sessions.reload()
    },
    [navigate, sessions],
  )

  const chat = useChatSession({
    sessionId,
    load: getChatSession,
    send: ({ sessionId: id, content }) =>
      deepSearch({ sessionId: id, content, mode: deepMode ? 'deep' : 'shallow' }),
    onSessionSettled,
  })

  const rows = mergeChatSession(sessions.data ?? [], justSettled)

  function openSession(session: ChatSession) {
    setRailOpen(false)
    void navigate({ to: '/search/$sessionId', params: { sessionId: session.id } })
  }

  function startNewChat() {
    setRailOpen(false)
    if (sessionId) {
      void navigate({ to: '/search' })
      return
    }
    chat.reset()
  }

  async function onRename(id: string, title: string) {
    try {
      setRailBusy(true)
      setRailError('')
      const updated = await renameChatSession(id, title)
      setJustSettled((current) => (current?.id === id ? updated : current))
      await sessions.reload()
    } catch (err) {
      setRailError(err instanceof Error ? err.message : 'Failed to rename the chat')
    } finally {
      setRailBusy(false)
    }
  }

  async function onDelete(session: ChatSession) {
    if (!window.confirm(`Delete "${session.title}"? This cannot be undone.`)) {
      return
    }
    try {
      setRailBusy(true)
      setRailError('')
      await deleteChatSession(session.id)
      setJustSettled((current) => (current?.id === session.id ? null : current))
      await sessions.reload()
      if (session.id === sessionId) {
        void navigate({ to: '/search', replace: true })
      }
    } catch (err) {
      setRailError(err instanceof Error ? err.message : 'Failed to delete the chat')
    } finally {
      setRailBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">Deep Search</h2>
          <p className="text-sm text-ink-soft">
            Ask in natural language. The AI expands keywords across your archive languages and
            searches document metadata and OCR text. Chats are saved.
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

      <Button
        variant="secondary"
        size="sm"
        aria-expanded={railOpen}
        onClick={() => setRailOpen((open) => !open)}
        className="self-start lg:hidden"
      >
        Chats ({rows.length})
      </Button>

      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:gap-6">
        {/* One instance across breakpoints, toggled by class: two would put the
            rows, their aria-current and their rename inputs into the
            accessibility tree twice. */}
        <aside className={`${railOpen ? 'block' : 'hidden'} lg:block lg:w-60 lg:shrink-0`}>
          <ChatSessionList
            sessions={rows}
            activeSessionId={sessionId}
            loading={sessions.loading}
            error={railError || sessions.error}
            busy={railBusy}
            newChatDisabled={!sessionId && chat.turns.length === 0}
            onSelect={openSession}
            onNewChat={startNewChat}
            onRename={onRename}
            onDelete={onDelete}
          />
        </aside>

        {/* min-w-0: without it a wide code block or an unbroken token in a
            markdown reply stretches this column past the page's max width. */}
        <div className="min-w-0 flex-1">
          {chat.loadError && <p className="mb-3 text-sm text-madder">{chat.loadError}</p>}
          {chat.unsaved && (
            <p className="mb-3 text-sm text-madder">
              This answer could not be saved, so the chat will not appear in your history.
            </p>
          )}
          <ChatPanel>
            <ChatTranscript
              turns={chat.turns}
              loading={chat.loading}
              sending={chat.sending}
              sendingLabel={deepMode ? 'Searching deeply...' : 'Searching...'}
              emptyHint='Try something like: "plumber invoice from last summer about the leak"'
              renderExtra={(turn) => <SearchHits turn={turn} />}
            />
            <ChatComposer
              value={chat.input}
              onChange={chat.setInput}
              onSubmit={() => void chat.submit()}
              placeholder="Describe what you are looking for..."
              submitLabel="Search"
              sendingLabel="Searching..."
              sending={chat.sending}
              disabled={chat.loading}
              error={chat.error}
              autoFocus
            />
          </ChatPanel>
        </div>
      </div>
    </section>
  )
}

function SearchHits({ turn }: { turn: ChatTurn }) {
  if (!turn.documents || turn.documents.length === 0) {
    return null
  }
  return (
    <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {turn.documents.map((doc) => (
        <SearchHitCard key={doc.id} document={doc} />
      ))}
    </div>
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
