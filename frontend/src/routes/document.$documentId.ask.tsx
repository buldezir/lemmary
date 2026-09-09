import { useCallback, useState } from 'react'
import { Link, useMatchRoute, useNavigate, useParams } from '@tanstack/react-router'
import { Button } from '../components/ui'
import { ChatPanel } from '../components/ChatPanel'
import { ChatTranscript } from '../components/ChatTranscript'
import { ChatComposer } from '../components/ChatComposer'
import { ChatSessionList } from '../components/ChatSessionList'
import { BindingOverride } from '../components/BindingOverride'
import { pb } from '../lib/pb'
import { ensureAuth } from '../lib/auth'
import { chatWithDocument } from '../lib/api/ai'
import {
  deleteChatSession,
  getChatSession,
  listChatSessions,
  mergeChatSession,
  renameChatSession,
  chatSessionBinding,
  type ChatSession,
} from '../lib/api/chats'
import type { ProviderBinding } from '../lib/api/providers'
import type { DocumentRecord } from '../lib/api/documents'
import { useAsync } from '../hooks/useAsync'
import { useChatSession } from '../hooks/useChatSession'

export function DocumentAskPage() {
  const { documentId } = useParams({ from: '/document/$documentId/ask' })
  const navigate = useNavigate()
  // See the note in search.tsx: the id sits on a child match, which useParams
  // cannot reach from the parent component.
  const matchRoute = useMatchRoute()
  const sessionMatch = matchRoute({ to: '/document/$documentId/ask/$sessionId' })
  const sessionId = sessionMatch ? (sessionMatch.sessionId as string) : undefined

  const [railOpen, setRailOpen] = useState(false)
  const [justSettled, setJustSettled] = useState<ChatSession | null>(null)
  const [railBusy, setRailBusy] = useState(false)
  const [railError, setRailError] = useState('')
  // The model this page will open its next conversation on. Not reset by
  // startNewChat: having picked a model once, the likely next thing is another
  // question for the same one.
  const [binding, setBinding] = useState<ProviderBinding | undefined>()

  const {
    data: document,
    loading,
    error: loadError,
  } = useAsync(async () => {
    await ensureAuth()
    return pb.collection('documents').getOne<DocumentRecord>(documentId)
  }, [documentId])

  const sessions = useAsync(
    () => listChatSessions({ kind: 'document', documentId }),
    [documentId],
  )

  const onSessionSettled = useCallback(
    (session: ChatSession, created: boolean) => {
      setJustSettled(session)
      if (created) {
        void navigate({
          to: '/document/$documentId/ask/$sessionId',
          params: { documentId, sessionId: session.id },
          replace: true,
        })
      }
      void sessions.reload()
    },
    [documentId, navigate, sessions],
  )

  const chat = useChatSession({
    sessionId,
    // A session id from another document's chat must not open here: it would
    // replay that conversation against this document's OCR text.
    load: async (id) => {
      const detail = await getChatSession(id)
      if (detail.session.document !== documentId) {
        throw new Error('That chat belongs to a different document.')
      }
      return detail
    },
    send: ({ sessionId: id, content }) =>
      chatWithDocument({ documentId, sessionId: id, content, binding }),
    onSessionSettled,
  })

  const hasOcrText = Boolean(document?.ocr_text?.trim())
  // A conversation keeps the binding its transcript was produced with -- the
  // server ignores anything else a request carries -- so once one exists the
  // picker reports it instead of offering to change it.
  //
  // Keyed on whether the URL names a conversation, not on whether one is
  // loaded. Those differ in the two cases that matter, in opposite directions:
  //
  //   - Opening someone's existing chat, the session is briefly null while it
  //     loads. Falling back to the local pick there is what showed the model
  //     from the *previous* chat on a conversation that never used it, until
  //     the page was reloaded.
  //   - During the send that creates a conversation there is no id and no
  //     session yet, and the local pick is genuinely what is answering, so
  //     showing nothing would blank the row mid-answer.
  const inConversation = Boolean(sessionId) || Boolean(chat.session)
  const shownBinding = inConversation ? chatSessionBinding(chat.session) : binding
  const bindingLocked = inConversation || chat.turns.length > 0
  const rows = mergeChatSession(sessions.data ?? [], justSettled)

  function openSession(session: ChatSession) {
    setRailOpen(false)
    void navigate({
      to: '/document/$documentId/ask/$sessionId',
      params: { documentId, sessionId: session.id },
    })
  }

  function startNewChat() {
    setRailOpen(false)
    if (sessionId) {
      void navigate({ to: '/document/$documentId/ask', params: { documentId } })
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
        void navigate({ to: '/document/$documentId/ask', params: { documentId }, replace: true })
      }
    } catch (err) {
      setRailError(err instanceof Error ? err.message : 'Failed to delete the chat')
    } finally {
      setRailBusy(false)
    }
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
          Questions are answered using the document&apos;s OCR text as context. Chats are saved.
        </p>
      </div>

      {!hasOcrText ? (
        <div className="rounded-none border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800">
          This document has no OCR text yet. Run full processing before asking questions.
        </div>
      ) : (
        <>
          <Button
            variant="secondary"
            size="sm"
            aria-expanded={railOpen}
            onClick={() => setRailOpen((open) => !open)}
            className="self-start lg:hidden"
          >
            Chats ({rows.length})
          </Button>

          <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:gap-3">
            <aside className={`${railOpen ? 'block' : 'hidden'} lg:block lg:w-52 lg:shrink-0`}>
              <ChatSessionList
                sessions={rows}
                activeSessionId={sessionId}
                loading={sessions.loading}
                error={railError || sessions.error}
                busy={railBusy}
                compact
                newChatDisabled={!sessionId && chat.turns.length === 0}
                onSelect={openSession}
                onNewChat={startNewChat}
                onRename={onRename}
                onDelete={onDelete}
              />
            </aside>

            <div className="min-w-0 flex-1">
              {chat.loadError && <p className="mb-3 text-sm text-madder">{chat.loadError}</p>}
              {chat.unsaved && (
                <p className="mb-3 text-sm text-madder">
                  {chat.unsavedDetail ||
                    'This answer could not be saved, so the chat will not appear in your history.'}
                </p>
              )}
              <ChatPanel>
                <ChatTranscript
                  conversationId={sessionId}
                  turns={chat.turns}
                  loading={chat.loading}
                  sending={chat.sending}
                  sendingLabel="Thinking..."
                  emptyHint='Ask a question about this document, for example: "What is the total amount?"'
                />
                <ChatComposer
                  value={chat.input}
                  onChange={chat.setInput}
                  onSubmit={() => void chat.submit()}
                  placeholder="Ask a question about this document..."
                  submitLabel="Send"
                  sendingLabel="Sending..."
                  sending={chat.sending}
                  disabled={chat.loading}
                  error={chat.error}
                  autoFocus
                />
              </ChatPanel>
            {/* Directly below the panel rather than inside it: ChatPanel is
                overflow-hidden -- which is what makes the transcript scroll
                instead of stretching the box -- and the model dropdown is
                absolutely positioned, so inside the panel its list was clipped
                at the panel's edge. border-t-0 butts this strip against the
                panel's bottom border, so it still reads as part of it. */}
              <div className="border border-t-0 border-line bg-surface px-4 py-3">
                <BindingOverride
                  label="Chat"
                  purpose="llm"
                  value={shownBinding}
                  onChange={setBinding}
                  locked={bindingLocked}
                  lockedHint="Fixed for this conversation. Start a new chat to ask a different model."
                  help="Answers questions about this document."
                  showConfigured
                />
              </div>
            </div>
          </div>
        </>
      )}
    </section>
  )
}
