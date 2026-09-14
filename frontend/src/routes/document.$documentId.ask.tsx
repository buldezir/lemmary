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
import { RunInFlightError } from '../lib/apiClient'
import {
  deleteChatSession,
  getChatSession,
  isRetryableFailure,
  listChatSessions,
  mergeChatSession,
  renameChatSession,
  waitForStoredTurn,
  chatSessionBinding,
  type ChatSession,
} from '../lib/api/chats'
import type { ProviderBinding } from '../lib/api/providers'
import type { DocumentRecord } from '../lib/api/documents'
import { useAsync } from '../hooks/useAsync'
import { useChatSession, type ChatSendResult } from '../hooks/useChatSession'
import { runId } from '../lib/runId'

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
  // The model the next conversation opens on, deliberately kept across a new chat.
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

  /**
   * The completion is detached from this request on the server, so a connection
   * that dies mid-answer has lost the delivery, not the answer: wait for the
   * turn to appear rather than report a failure over paid-for work.
   */
  const ask = useCallback(
    async (id: string | undefined, content: string): Promise<ChatSendResult> => {
      const requestId = runId()
      try {
        return await chatWithDocument({
          documentId,
          sessionId: id,
          content,
          runId: requestId,
          binding,
        })
      } catch (err) {
        // Any failure, not only a dropped socket: a reverse proxy that gives up
        // answers 502/504 while the server keeps working, and only the
        // transcript can tell that from a genuine refusal.
        const stored = id ? await waitForStoredTurn(id, requestId) : null
        if (stored) {
          return { session: stored.session, message: stored.message, saved: true }
        }
        // A chat this send opened has an id only the server knows, so there is
        // nothing to wait on and the rail refresh is what makes it reachable.
        // Reported as a run in flight, not a failed send, since handing the
        // question back would invite paying for it twice.
        // ponytail: a first prompt cannot be recovered on the page until the
        // session id is on the wire before the completion, as search does with
        // its `session` frame; convert this endpoint to SSE when that matters.
        if (!id && isRetryableFailure(err)) {
          void sessions.reload()
          throw new RunInFlightError(err)
        }
        throw err
      }
    },
    [binding, documentId, sessions],
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
    send: ({ sessionId: id, content }) => ask(id, content),
    onSessionSettled,
  })

  const hasOcrText = Boolean(document?.ocr_text?.trim())
  // A conversation keeps the binding its transcript was produced with, and the
  // server ignores anything else a request carries.
  //
  // Keyed on whether the URL names a conversation rather than on whether one is
  // loaded: an existing chat's session is briefly null while it loads, and
  // during the send that creates one there is no id yet but the local pick is
  // genuinely what is answering.
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
            {/* Below the panel rather than inside it: ChatPanel is
                overflow-hidden so the transcript scrolls, which clips the
                absolutely positioned model dropdown. border-t-0 keeps it
                reading as part of the panel. */}
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
