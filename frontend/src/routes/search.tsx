import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useMatchRoute, useNavigate } from '@tanstack/react-router'
import { Button } from '../components/ui'
import { ChatPanel } from '../components/ChatPanel'
import { ChatTranscript } from '../components/ChatTranscript'
import { ChatComposer } from '../components/ChatComposer'
import { ChatSessionList } from '../components/ChatSessionList'
import { MarkdownContent } from '../components/MarkdownContent'
import { runId } from '../lib/runId'
import { RunInFlightError } from '../lib/apiClient'
import { useAsync } from '../hooks/useAsync'
import { useChatSession, type ChatSendResult } from '../hooks/useChatSession'
import { BindingOverride } from '../components/BindingOverride'
import { WebSearchToggle } from '../components/WebSearchToggle'
import type { ProviderBinding } from '../lib/api/providers'
import {
  cancelSearchRun,
  searchStream,
  type ResearchEvent,
  type SearchMode,
} from '../lib/api/ai'
import {
  deleteChatSession,
  getChatSession,
  listChatSessions,
  mergeChatSession,
  renameChatSession,
  waitForStoredTurn,
  chatSessionBinding,
  type ChatSession,
  type ChatTurn,
  type SearchDocumentHit,
} from '../lib/api/chats'
import { applyStep, type ResearchStep } from '../lib/researchSteps'
import { formatContextUsage, type ContextUsage } from '../lib/contextUsage'

const modes: {
  value: SearchMode
  label: string
  to: '/rag/search' | '/rag/research'
  hint: string
}[] = [
  { value: 'search', label: 'Search', to: '/rag/search', hint: 'Find documents and list them.' },
  {
    value: 'research',
    label: 'Research',
    to: '/rag/research',
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
  const navigate = useNavigate()
  // The session id lives on a child route, so useParams cannot see it here.
  // matchRoute also hands back a fresh object each render, so the id is
  // destructured out before anything depends on it.
  const matchRoute = useMatchRoute()
  const research = Boolean(matchRoute({ to: '/rag/research', fuzzy: true }))
  const mode: SearchMode = research ? 'research' : 'search'
  const basePath = research ? '/rag/research' : '/rag/search'
  const sessionMatch = research
    ? matchRoute({ to: '/rag/research/$sessionId' })
    : matchRoute({ to: '/rag/search/$sessionId' })
  const sessionId = sessionMatch ? (sessionMatch.sessionId as string) : undefined

  const [railOpen, setRailOpen] = useState(false)
  const [justSettled, setJustSettled] = useState<ChatSession | null>(null)
  const [railBusy, setRailBusy] = useState(false)
  const [railError, setRailError] = useState('')
  // The model the next conversation opens on, deliberately kept across a new chat.
  const [binding, setBinding] = useState<ProviderBinding | undefined>()
  // Per turn, not per conversation: the server stores nothing about it, so a
  // reload starts from off. Off is the safe direction for a metered tool.
  const [web, setWeb] = useState(false)
  const [steps, setSteps] = useState<ResearchStep[]>([])
  const [draft, setDraft] = useState('')
  const [liveUsage, setLiveUsage] = useState<ContextUsage | null>(null)
  // The controller only abandons this page's view of the run; the id is what
  // stops the run itself. Both are needed, because the server keeps working
  // through a dropped connection.
  const runRef = useRef<{ controller: AbortController; id: string } | null>(null)

  const sessions = useAsync(() => listChatSessions({ kind: 'search' }), [])

  // Stops this page painting the run, deliberately not the run itself: an
  // answer must not be thrown away just because nobody was watching it arrive.
  // Cancelling for real is `endRun`.
  useEffect(() => () => runRef.current?.controller.abort(), [])

  /**
   * Ends the run that owns the screen. Switching conversations does not unmount
   * this page, so without this a run keeps painting its steps over whichever
   * transcript replaced it. The rail is reloaded either way, because the abort
   * can land just after the turn was saved.
   */
  const endRun = useCallback(() => {
    const run = runRef.current
    if (!run) {
      return
    }
    run.controller.abort()
    // Said out loud, because hanging up does not stop the run any more: it
    // would finish and store a turn for a conversation already left.
    void cancelSearchRun({ runId: run.id })
    runRef.current = null
    void sessions.reload()
  }, [sessions])

  const onSessionSettled = useCallback(
    (session: ChatSession, created: boolean) => {
      // Merged in straight away so the row is there with the transcript.
      setJustSettled(session)
      if (created) {
        // replace: Back should not land on the now-orphaned empty /search.
        void navigate({
          to: `${basePath}/$sessionId`,
          params: { sessionId: session.id },
          replace: true,
        })
      }
      // After every turn, not only the first: last_message_at moved and the row
      // has to move with it. reload() refreshes without a loading flash.
      void sessions.reload()
    },
    [basePath, navigate, sessions],
  )

  /**
   * Runs a turn as a stream, resolving with the stored turn so the conversation
   * hook treats it like any other send. Both modes stream: research for its
   * steps, plain search for the heartbeat underneath, without which a slow
   * answer is hung up on by anything with a read timeout in between.
   */
  const runTurn = useCallback(
    async (
      id: string | undefined,
      content: string,
      turnMode: SearchMode,
      turnBinding: ProviderBinding | undefined,
      turnWeb: boolean,
    ): Promise<ChatSendResult> => {
      const run = { controller: new AbortController(), id: runId() }
      runRef.current = run

      // Collected outside React state as well: the finished turn is assembled
      // from these, and state updates are not readable synchronously.
      const collected: ResearchStep[] = []
      let answer = ''
      let streamError = ''
      let incomplete = false
      // In a box rather than a plain `let`: TypeScript cannot see an assignment
      // made inside the stream callback and would narrow the variable to null.
      const box: {
        stored: Extract<ResearchEvent, { type: 'saved' }> | null
        session: ChatSession | null
      } = { stored: null, session: null }

      try {
        await searchStream(
          {
            sessionId: id,
            content,
            mode: turnMode,
            runId: run.id,
            // Research only: the server ignores it in search mode, and the
            // toggle is not rendered there either.
            web: turnMode === 'research' && turnWeb,
            binding: turnBinding,
          },
          (event) => {
            switch (event.type) {
              case 'session':
                box.session = event.session
                break
              case 'step':
                applyStep(collected, event)
                setSteps([...collected])
                break
              case 'delta':
                answer += event.content
                setDraft(answer)
                break
              case 'usage':
                setLiveUsage({
                  peak_prompt: event.prompt_tokens,
                  context_window: event.context_window,
                  estimated: event.estimated,
                })
                break
              case 'message':
                answer = event.content
                incomplete = event.incomplete ?? false
                setDraft(answer)
                break
              case 'saved':
                box.stored = event
                break
              case 'error':
                streamError = event.message
                break
              default:
                break
            }
          },
          run.controller.signal,
        )
      } catch (err) {
        // A turn that was already stored is not a failed send, however the
        // stream ended: `saved` can be the last event before a drop, and
        // treating that as a failure buys a second paid run and a second chat.
        if (box.stored) {
          streamError = ''
        } else if (run.controller.signal.aborted) {
          throw cancelled(turnMode, err)
        } else if (err instanceof RunInFlightError && (box.session ?? id)) {
          // The connection died, the run did not: the server stores the turn
          // regardless, so waiting for it turns a lost connection back into a
          // normal one. Keep the live steps up until the wait returns, or the
          // whole run looks thrown away.
          box.stored = await recoverTurn(
            box.session?.id ?? (id as string),
            run.id,
            run.controller.signal,
          )
          if (!box.stored) {
            throw run.controller.signal.aborted
              ? cancelled(turnMode, err)
              : new Error(interruptedWithoutAnswerMessage, { cause: err })
          }
          streamError = ''
        } else {
          throw err
        }
      } finally {
        if (runRef.current === run) {
          runRef.current = null
        }
        setSteps([])
        setDraft('')
        setLiveUsage(null)
      }

      if (streamError) {
        throw new Error(streamError)
      }
      const stored = box.stored
      if (!stored) {
        throw new Error(
          turnMode === 'research'
            ? 'The research run ended without an answer.'
            : 'The search ended without an answer.',
        )
      }

      return {
        session: stored.session,
        message: stored.message.incomplete || !incomplete
          ? stored.message
          : { ...stored.message, incomplete: true },
        documents: stored.documents,
        saved: stored.saved,
        detail: stored.detail,
      }
    },
    [],
  )

  const chat = useChatSession({
    sessionId,
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
    send: ({ sessionId: id, content }) => runTurn(id, content, mode, binding, web),
    onSessionSettled,
  })

  // A chat's stored mode wins over the path, which a hand-edited or stale URL
  // can contradict, so the next turn is not sent under a mode the server refuses.
  const loadedMode = chat.session?.mode
  useEffect(() => {
    if (!sessionId || !loadedMode || loadedMode === mode) {
      return
    }
    void navigate({
      to: loadedMode === 'research' ? '/rag/research/$sessionId' : '/rag/search/$sessionId',
      params: { sessionId },
      replace: true,
    })
  }, [loadedMode, mode, navigate, sessionId])

  const rows = mergeChatSession(sessions.data ?? [], justSettled)
  const active = modes.find((item) => item.value === mode) ?? modes[0]
  // Locked from the first turn, including the one in flight, whose request
  // already carries the mode it was sent under.
  const locked = Boolean(sessionId) || chat.sending
  // The binding is fixed for a conversation for the same reason mode is: the
  // replayed transcript was produced by one model, and answering with another
  // reads that work back as its own.
  //
  // Keyed on whether the URL names a conversation rather than on whether one is
  // loaded: an existing chat's session is briefly null while it loads, and
  // during the send that creates one there is no id yet but the local pick is
  // genuinely what is answering.
  const inConversation = Boolean(sessionId) || Boolean(chat.session)
  const shownBinding = inConversation ? chatSessionBinding(chat.session) : binding
  const bindingLocked = inConversation || chat.sending || chat.turns.length > 0

  // A chat opens in the mode its last turn ran in: continuing a research
  // conversation as a plain search answers a different question than the
  // transcript above it.
  function openSession(session: ChatSession) {
    setRailOpen(false)
    endRun()
    void navigate({
      to: session.mode === 'research' ? '/rag/research/$sessionId' : '/rag/search/$sessionId',
      params: { sessionId: session.id },
    })
  }

  function startNewChat() {
    setRailOpen(false)
    endRun()
    if (sessionId) {
      void navigate({ to: basePath })
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
      if (session.id === sessionId) {
        endRun()
      }
      await deleteChatSession(session.id)
      setJustSettled((current) => (current?.id === session.id ? null : current))
      await sessions.reload()
      if (session.id === sessionId) {
        void navigate({ to: basePath, replace: true })
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
            {active.hint}{' '}
            {locked ? 'A chat stays in the mode it started in.' : 'Chats are saved.'}
          </p>
        </div>
        {/* Links rather than a radiogroup, so back, bookmark and open-in-new-tab
            work on them. They stop being links once the chat exists: see the
            mode lock above. */}
        <ModeSwitch mode={mode} locked={locked} />
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

      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:gap-3">
        {/* One instance across breakpoints, toggled by class: two would put the
            rows and their rename inputs into the accessibility tree twice. */}
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
              sendingLabel="Searching..."
              emptyHint={`Try something like: "${examples[mode]}"`}
              renderBefore={(turn) => {
                return turn.steps && turn.steps.length > 0 ? (
                  <StepList steps={turn.steps} collapsed />
                ) : null
              }}
              renderExtra={(turn) => (
                <>
                  {turn.incomplete && <IncompleteNotice />}
                  {turn.usage && <ContextUsageNotice usage={turn.usage} />}
                  {mode === 'search' && <SearchHits turn={turn} />}
                </>
              )}
              renderSending={
                mode === 'research'
                  ? () => (
                      <div className="space-y-3">
                        <StepList steps={steps} />
                        {liveUsage && <LiveContextUsage usage={liveUsage} />}
                        {draft && (
                          <div className="flex justify-start">
                            <div className="max-w-[85%] rounded-none border border-line bg-paper px-4 py-2.5 text-sm leading-relaxed text-ink">
                              <MarkdownContent content={draft} />
                            </div>
                          </div>
                        )}
                      </div>
                    )
                  : undefined
              }
            />
            <ChatComposer
              value={chat.input}
              onChange={chat.setInput}
              onSubmit={() => void chat.submit()}
              placeholder={placeholders[mode]}
              submitLabel={active.label}
              sendingLabel={mode === 'research' ? 'Researching...' : 'Searching...'}
              sending={chat.sending}
              disabled={chat.loading}
              error={chat.error}
              // A run this page is only watching -- it was started before a
              // reload -- has no run id here, so it is stopped by conversation.
              onCancel={
                chat.resuming && sessionId
                  ? () => void cancelSearchRun({ sessionId })
                  : endRun
              }
              autoFocus
            />
          </ChatPanel>
          {/* Below the panel rather than inside it: ChatPanel is overflow-hidden
              so the transcript scrolls, which clips the absolutely positioned
              model dropdown. border-t-0 keeps it reading as part of the panel. */}
          <div className="border border-t-0 border-line bg-surface px-4 py-3">
            {mode === 'research' && (
              <div className="mb-3">
                <WebSearchToggle checked={web} onChange={setWeb} disabled={chat.sending} />
              </div>
            )}
            <BindingOverride
              label="Search"
              purpose="llm"
              value={shownBinding}
              onChange={setBinding}
              locked={bindingLocked}
              lockedHint="Fixed for this chat. Start a new one to search with a different model."
              help="Drives the search or research loop. The helper model that reads documents in bulk keeps its own binding in Settings."
              showConfigured
            />
          </div>
        </div>
      </div>
    </section>
  )
}

/**
 * Not `streamConnectionLostMessage`, which promises the answer will be in the
 * chat history: by the time this is reached no turn was stored.
 */
const interruptedWithoutAnswerMessage =
  'The connection was interrupted and the run ended without an answer. Try again.'

/** Cancelling is not a provider failure, and fetch reports it as a DOMException nobody wants to read. */
function cancelled(mode: SearchMode, cause: unknown) {
  return new Error(mode === 'research' ? 'Research cancelled.' : 'Search cancelled.', { cause })
}

/**
 * Collects the turn a dropped stream never delivered, shaped like the `saved`
 * event it stands in for. Null when the wait ran out and nothing was stored.
 */
async function recoverTurn(
  sessionId: string,
  runId: string,
  signal: AbortSignal,
): Promise<Extract<ResearchEvent, { type: 'saved' }> | null> {
  const stored = await waitForStoredTurn(sessionId, runId, { signal })
  if (!stored) {
    return null
  }
  return {
    type: 'saved',
    session: stored.session,
    message: stored.message,
    documents: stored.message.documents,
    saved: true,
  }
}

function IncompleteNotice() {
  return (
    <p className="border-t border-line pt-2 text-xs text-ink-muted">
      This answer was cut off before it finished. Ask again to get the rest.
    </p>
  )
}

function ContextUsageNotice({ usage }: { usage: ContextUsage }) {
  const text = formatContextUsage(usage)
  if (!text) return null
  return <p className="border-t border-line pt-2 text-xs text-ink-muted">Context used: {text}</p>
}

function LiveContextUsage({ usage }: { usage: ContextUsage }) {
  const text = formatContextUsage(usage)
  if (!text) return null
  return (
    <p className="border-l-2 border-line pl-3 text-xs text-ink-faint tabular-nums">
      Context: {text}
    </p>
  )
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

function ModeSwitch({ mode, locked }: { mode: SearchMode; locked: boolean }) {
  const className = (item: (typeof modes)[number]) =>
    `px-3 py-1.5 text-sm transition-colors ${
      mode === item.value ? 'bg-ink text-paper' : 'text-ink-muted'
    }`

  if (locked) {
    return (
      <div
        role="group"
        aria-label="Search mode"
        title="A chat stays in the mode it started in. Start a new chat to switch."
        className="flex rounded-xs border border-line bg-surface p-1"
      >
        {modes.map((item) => (
          <span
            key={item.value}
            aria-current={mode === item.value ? 'true' : undefined}
            className={`${className(item)} ${mode === item.value ? '' : 'opacity-40'}`}
          >
            {item.label}
          </span>
        ))}
      </div>
    )
  }

  return (
    <nav aria-label="Search mode" className="flex rounded-xs border border-line bg-surface p-1">
      {modes.map((item) => (
        <Link
          key={item.value}
          to={item.to}
          aria-current={mode === item.value ? 'page' : undefined}
          className={`${className(item)} ${mode === item.value ? '' : 'hover:text-ink'}`}
        >
          {item.label}
        </Link>
      ))}
    </nav>
  )
}

/**
 * The documents behind an answer, in Search mode only. Research cites what it
 * read inline, so drawing every hit beside those citations -- including the
 * ones it looked at and discarded -- would make the answer look better
 * evidenced than it is.
 */
function SearchHits({ turn }: { turn: ChatTurn }) {
  if (!turn.documents || turn.documents.length === 0) {
    return null
  }
  return (
    <div data-testid="search-hits" className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {turn.documents.map((doc) => (
        <SearchHitCard key={doc.id} document={doc} />
      ))}
    </div>
  )
}

function SearchHitCard({ document }: { document: SearchDocumentHit }) {
  const meta = [document.document_type, document.correspondent].filter(Boolean).join(' · ')
  // Shown only when the extraction knew a page, which no current OCR provider
  // reports, so in practice this never renders today.
  const page = document.passages?.[0]?.page

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
        {page ? <span className="mr-1 font-mono text-ink-soft">p. {page}</span> : null}
        {document.ocr_snippet || document.passages?.[0]?.text || document.summary || 'No preview.'}
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
