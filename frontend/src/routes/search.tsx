import { useEffect } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'

import { ChatPanel } from '../components/ChatPanel'
import { ChatTranscript } from '../components/ChatTranscript'
import { ChatComposer } from '../components/ChatComposer'
import { ChatWorkspaceFrame } from '../components/ChatWorkspaceFrame'
import { BindingOverride } from '../components/BindingOverride'
import { useChatSession } from '../hooks/useChatSession'
import { useChatWorkspace } from '../hooks/useChatWorkspace'
import { cancelSearchRun } from '../lib/api/ai'
import { chatSessionBinding, getChatSession, type ChatTurn } from '../lib/api/chats'

/**
 * Search: one round against the archive, answered as a list of what it found.
 * No tools beyond the search, nothing kept between turns, nothing to resume.
 * Research is the other page, and shares only the frame around this one.
 */
export function SearchPage() {
  const navigate = useNavigate()
  const ws = useChatWorkspace({ mode: 'search', basePath: '/rag/search' })

  const chat = useChatSession({
    sessionId: ws.sessionId,
    // A document chat's id must not open here: replaying its one-document
    // transcript into a search turn asks the archive a question never put to
    // it. The Ask AI page makes the mirror check.
    load: async (id) => {
      const detail = await getChatSession(id)
      if (detail.session.kind !== 'search') {
        throw new Error('That chat belongs to a different page.')
      }
      return detail
    },
    send: ({ sessionId: id, content }) =>
      ws.runTurn({ sessionId: id, content, binding: ws.binding }),
    onSessionSettled: ws.onSessionSettled,
  })
  const { setAdopt } = ws
  useEffect(() => setAdopt(chat.adoptSession), [chat.adoptSession, setAdopt])

  // A chat's stored mode wins over the path, which a hand-edited or stale URL
  // can contradict, so the next turn is not sent under a mode the server refuses.
  const loadedMode = chat.session?.mode
  useEffect(() => {
    if (!ws.sessionId || loadedMode !== 'research') {
      return
    }
    void navigate({
      to: '/rag/research/$sessionId',
      params: { sessionId: ws.sessionId },
      replace: true,
    })
  }, [loadedMode, navigate, ws.sessionId])

  const inConversation = Boolean(ws.sessionId) || Boolean(chat.session)
  const shownBinding = inConversation ? chatSessionBinding(chat.session) : ws.binding

  function startNewChat() {
    ws.setRailOpen(false)
    ws.endRun()
    if (ws.sessionId) {
      void navigate({ to: '/rag/search' })
      return
    }
    chat.reset()
  }

  return (
    <ChatWorkspaceFrame
      title="AI assisted search"
      hint="Find documents and list them. Only completed documents are searched."
      rows={ws.rows}
      sessionId={ws.sessionId}
      sessionsLoading={ws.sessions.loading}
      sessionsError={ws.sessions.error}
      railOpen={ws.railOpen}
      onToggleRail={() => ws.setRailOpen(!ws.railOpen)}
      railBusy={ws.railBusy}
      railError={ws.railError}
      newChatDisabled={!ws.sessionId && chat.turns.length === 0}
      onSelect={ws.openSession}
      onNewChat={startNewChat}
      onRename={ws.onRename}
      onDelete={ws.onDelete}
    >
      {chat.loadError && <p className="mb-3 text-sm text-madder">{chat.loadError}</p>}
      {chat.unsaved && (
        <p className="mb-3 text-sm text-madder">
          {chat.unsavedDetail ||
            'This answer could not be saved, so the chat will not appear in your history.'}
        </p>
      )}
      <ChatPanel>
        <ChatTranscript
          conversationId={ws.sessionId}
          turns={chat.turns}
          loading={chat.loading}
          sending={chat.sending}
          sendingLabel="Searching..."
          emptyHint={'Try something like: "plumber invoice from last summer about the leak"'}
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
          // A run this page is only watching -- it was started before a reload
          // -- has no run id here, so it is stopped by conversation.
          onCancel={
            chat.resuming && ws.sessionId
              ? () => void cancelSearchRun({ sessionId: ws.sessionId as string })
              : ws.endRun
          }
          autoFocus
        />
      </ChatPanel>
      {/* Below the panel rather than inside it: ChatPanel is overflow-hidden so
          the transcript scrolls, which clips the absolutely positioned model
          dropdown. border-t-0 keeps it reading as part of the panel. */}
      <div className="border border-t-0 border-line bg-surface px-4 py-3">
        <BindingOverride
          label="Search"
          purpose="llm"
          value={shownBinding}
          onChange={ws.setBinding}
          locked={inConversation || chat.sending || chat.turns.length > 0}
          lockedHint="Fixed for this chat. Start a new one to search with a different model."
          help="Answers this search in place of the General AI model from Settings."
          showConfigured
        />
      </div>
    </ChatWorkspaceFrame>
  )
}

/**
 * The documents behind an answer. Search mode only: research cites what it read
 * inline, so drawing every hit beside those citations -- including the ones it
 * looked at and discarded -- would make the answer look better evidenced than
 * it is.
 */
function SearchHits({ turn }: { turn: ChatTurn }) {
  if (!turn.documents || turn.documents.length === 0) {
    return null
  }
  return (
    <div data-testid="search-hits" className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {turn.documents.map((doc) => (
        <Link
          key={doc.id}
          to="/document/$documentId"
          params={{ documentId: doc.id }}
          className="border border-line bg-paper p-3 transition-colors hover:border-ink"
        >
          <p className="text-sm font-medium text-ink">{doc.title}</p>
          {doc.document_date && (
            <p className="font-mono text-xs tabular-nums text-ink-soft">{doc.document_date}</p>
          )}
          {doc.ocr_snippet && <p className="mt-1 text-xs text-ink-muted">{doc.ocr_snippet}</p>}
          {doc.tags && doc.tags.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1">
              {doc.tags.map((tag) => (
                <span
                  key={tag}
                  className="border border-line px-1.5 py-0.5 text-[11px] text-ink-muted"
                >
                  {tag}
                </span>
              ))}
            </div>
          )}
        </Link>
      ))}
    </div>
  )
}
