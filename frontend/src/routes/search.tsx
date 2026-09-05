import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useMatchRoute, useNavigate } from '@tanstack/react-router'
import { Button } from '../components/ui'
import { ChatPanel } from '../components/ChatPanel'
import { ChatTranscript } from '../components/ChatTranscript'
import { ChatComposer } from '../components/ChatComposer'
import { ChatSessionList } from '../components/ChatSessionList'
import { MarkdownContent } from '../components/MarkdownContent'
import { useAsync } from '../hooks/useAsync'
import { useChatSession, type ChatSendResult } from '../hooks/useChatSession'
import {
  cancelSearchRun,
  searchStream,
  type ResearchEvent,
  type ResearchStepKind,
  type SearchMode,
} from '../lib/api/ai'
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

type ResearchStep = {
  kind: ResearchStepKind
  label: string
  done: boolean
}

/**
 * What a research run produced beyond its text. Neither is stored with the
 * turn — the steps are a record of this run, and `incomplete` describes this
 * generation rather than the answer — so both are kept in memory, keyed by the
 * stored message, and are gone when the chat is reopened.
 */
type TurnExtras = {
  steps: ResearchStep[]
  incomplete: boolean
}

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
  // The mode is the path: /rag/search lists documents, /rag/research reads them
  // and answers. Both render this page, so a reload, a bookmark and a shared
  // link all carry the mode with them, and there is no state to keep in step.
  //
  // The session id lives on a child route, and a child's params are invisible
  // to useParams from here — the closest match is /rag/search, which has none.
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
  // Live progress of a research run, cleared when it ends.
  const [steps, setSteps] = useState<ResearchStep[]>([])
  const [draft, setDraft] = useState('')
  const [extras, setExtras] = useState<Record<string, TurnExtras>>({})
  // A research run outlives an unmount unless it is cancelled: the fetch keeps
  // the stream open and the server keeps calling the provider.
  // The controller abandons this page's view of the run; the id is what stops
  // the run itself. Both are needed: since the server keeps working through a
  // dropped connection, letting go of the stream no longer cancels anything.
  const runRef = useRef<{ controller: AbortController; id: string } | null>(null)

  const sessions = useAsync(() => listChatSessions({ kind: 'search' }), [])

  // Unmounting stops this page painting the run. It deliberately does not stop
  // the run: leaving the page, closing the tab and losing the network are the
  // same event to everything downstream, and cancelling here would restore the
  // exact behaviour this change removed -- an answer thrown away because
  // nobody was watching it arrive. Cancelling is `endRun`, which is reached
  // only by someone actually asking for it.
  useEffect(() => () => runRef.current?.controller.abort(), [])

  /**
   * Ends the run that owns the screen.
   *
   * Switching conversations does not unmount this page — the session id lives
   * on a child route — so without this a run started in one chat keeps painting
   * its steps and its streamed draft over whichever transcript replaced it,
   * and keeps that chat's composer disabled until the provider is done.
   *
   * The server drops a cancelled research turn rather than storing it, but the
   * abort can land in the moment after it was saved, so the rail is refreshed
   * either way: a chat that did get written is in the list rather than missing
   * until the next full reload.
   */
  const endRun = useCallback(() => {
    const run = runRef.current
    if (!run) {
      return
    }
    run.controller.abort()
    // Said out loud, because hanging up does not stop it any more. Without
    // this the abandoned run would go on to finish and store a turn for a
    // conversation the user has already left.
    void cancelSearchRun(run.id)
    runRef.current = null
    void sessions.reload()
  }, [sessions])

  const onSessionSettled = useCallback(
    (session: ChatSession, created: boolean) => {
      // Merged in straight away so the row is there with the transcript, not a
      // round trip later.
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
   * hook treats it like any other send. The steps and the streamed draft are
   * this page's own state — they belong to a run in progress, not to the
   * transcript.
   *
   * Both modes go through here. Research needs the stream for its steps;
   * plain search needs it for the heartbeat underneath, having previously been
   * a POST that stayed silent until the answer was ready and so was liable to
   * be hung up on by anything with a read timeout in between.
   */
  const runTurn = useCallback(
    async (id: string | undefined, content: string, turnMode: SearchMode): Promise<ChatSendResult> => {
      const run = { controller: new AbortController(), id: crypto.randomUUID() }
      runRef.current = run

      // Collected outside React state as well: the finished turn is assembled
      // from these, and state updates are not readable synchronously.
      const collected: ResearchStep[] = []
      let answer = ''
      let streamError = ''
      let incomplete = false
      // In a box rather than a plain `let`: TypeScript cannot see an assignment
      // made inside the stream callback and would narrow the variable to null.
      const box: { stored: Extract<ResearchEvent, { type: 'saved' }> | null } = { stored: null }

      try {
        await searchStream(
          { sessionId: id, content, mode: turnMode, runId: run.id },
          (event) => {
            switch (event.type) {
              case 'step':
                applyStep(collected, event)
                setSteps([...collected])
                break
              case 'delta':
                answer += event.content
                setDraft(answer)
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
        // stream ended.
        //
        // The `saved` event can be the last thing to arrive before a drop, and
        // treating that as a failure was expensive: the composer came back
        // with the question still in it, the answer vanished, and retrying
        // bought a second run of research that had already been paid for --
        // plus a second chat, since the session is created before the agent
        // starts and the page has not learnt its id yet.
        if (box.stored) {
          streamError = ''
        } else if (run.controller.signal.aborted) {
          // Cancelling is not a provider failure, and the fetch reports it as
          // a DOMException nobody wants to read.
          throw new Error(turnMode === 'research' ? 'Research cancelled.' : 'Search cancelled.', {
            cause: err,
          })
        } else {
          throw err
        }
      } finally {
        if (runRef.current === run) {
          runRef.current = null
        }
        setSteps([])
        setDraft('')
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

      const finished = collected.map((step) => ({ ...step, done: true }))
      // Keyed by the stored message. An unsaved turn has no id to key on, and
      // its steps are simply not shown — the unsaved notice is the thing that
      // matters there.
      if (stored.message.id) {
        setExtras((current) => ({
          ...current,
          [stored.message.id]: { steps: finished, incomplete },
        }))
      }
      return {
        session: stored.session,
        message: stored.message,
        documents: stored.documents,
        saved: stored.saved,
        detail: stored.detail,
      }
    },
    [],
  )

  const chat = useChatSession({
    sessionId,
    // A document chat's id must not open here: its transcript is about one
    // document's OCR text, and replaying it into a search turn asks the archive
    // a question that was never put to it. The Ask AI page makes the mirror
    // check.
    load: async (id) => {
      const detail = await getChatSession(id)
      if (detail.session.kind !== 'search') {
        throw new Error('That chat belongs to a different page.')
      }
      return detail
    },
    send: ({ sessionId: id, content }) => runTurn(id, content, mode),
    onSessionSettled,
  })

  // The path says which mode, but a chat's stored mode is what it actually is,
  // and the two can disagree — a hand-edited URL, or a link to /rag/search/<id>
  // for a chat that turns out to be Research. Corrected here rather than obeyed,
  // so the next turn is not sent under a mode the server would refuse.
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
  // A chat is locked to its mode from the moment it has one turn — including
  // the turn still in flight, whose request already carries the mode it was
  // sent under.
  const locked = Boolean(sessionId) || chat.sending

  // A chat opens in the mode its last turn ran in, which is also the path it
  // lives on: continuing a research conversation as a plain search would answer
  // a different question than the one above it in the transcript.
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
        {/* Links, not a radiogroup: each mode is a path, so these navigate —
            which is also what makes the back button, a bookmark and an
            open-in-new-tab work on them.

            They stop being links once the chat exists. A transcript is a
            sequence: its answers were produced by one mode, and the next turn
            reads them back to the model as its own prior work. Switching
            underneath that would answer a later question in a way the earlier
            ones do not support, so the way to the other mode is a new chat. */}
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
                const turnSteps = extras[turn.id]?.steps
                return turnSteps && turnSteps.length > 0 ? (
                  <StepList steps={turnSteps} collapsed />
                ) : null
              }}
              renderExtra={(turn) => (
                <>
                  {extras[turn.id]?.incomplete && <IncompleteNotice />}
                  {mode === 'search' && <SearchHits turn={turn} />}
                </>
              )}
              renderSending={
                mode === 'research'
                  ? () => (
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
              // A run can take a while, so there has to be a way out of one
              // that is taking too long. Research most of all, but a search
              // waiting on a slow provider is no different to sit through.
              onCancel={endRun}
              autoFocus
            />
          </ChatPanel>
        </div>
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
  if (event.status === 'progress') {
    // A running count rewrites the pending line in place; a progress event
    // with nothing pending is a stray and is dropped rather than shown twice.
    if (pending) pending.label = progressLabel(event)
    return
  }
  if (!pending) {
    steps.push({ kind: event.kind, label: doneLabel(event), done: true })
    return
  }
  pending.label = doneLabel(event, pending.label)
  pending.done = true
}

function plural(n: number, noun: string) {
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}

function startLabel(event: Extract<ResearchEvent, { type: 'step' }>) {
  switch (event.kind) {
    case 'search':
      return event.query ? `Searching “${event.query}”` : 'Searching'
    case 'read':
      return `Reading ${plural(event.count ?? 0, 'document')}`
    case 'survey':
      return event.query ? `Surveying documents for “${event.query}”` : 'Surveying documents'
    case 'count':
      return event.query ? `Counting documents matching “${event.query}”` : 'Counting documents'
    default:
      return 'Writing answer'
  }
}

function progressLabel(event: Extract<ResearchEvent, { type: 'step' }>) {
  const total = event.count ?? 0
  const done = event.done ?? 0
  return total > 0 ? `Surveyed ${done} of ${plural(total, 'document')}` : 'Surveying documents'
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
      const verb = event.distilled ? 'Read and summarised' : 'Read'
      return titles.length > 0 ? `${verb} ${shown}${rest}` : (fallback ?? `${verb} documents`)
    }
    case 'survey': {
      const surveyed = `Surveyed ${plural(event.count ?? 0, 'document')}`
      return event.query ? `${surveyed} for “${event.query}”` : surveyed
    }
    case 'count': {
      const counted = `Counted ${plural(event.count ?? 0, 'document')}`
      return event.query ? `${counted} matching “${event.query}”` : counted
    }
    default:
      return 'Answer written'
  }
}

/**
 * Shown under an answer whose generation was cut off. The text above it is
 * real as far as it goes, which is exactly why it needs saying: a partial
 * answer reads like a complete one.
 */
function IncompleteNotice() {
  return (
    <p className="border-t border-line pt-2 text-xs text-ink-muted">
      This answer was cut off before it finished. Ask again to get the rest.
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

/**
 * The two modes, as the two paths they are — or, once the chat is under way,
 * as a plain statement of which one it is in.
 */
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
 * The documents behind an answer, in Search mode only.
 *
 * In Search the cards are the answer: the mode's whole job is to find documents
 * and list them. Research answers in prose and cites what it actually read
 * inline, so the same grid there would restate the citations and, worse, show
 * every document the search turned up beside them — including the ones the run
 * looked at and discarded. A reader cannot tell those apart from sources, so
 * the cards would make the answer look better evidenced than it is. The hits
 * are still stored with the turn either way; Research just does not draw them.
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
  // The snippet is the best matching passage, shortened by the backend. The
  // page is shown only when the extraction knew one, which no current OCR
  // provider reports -- so in practice this is never rendered today.
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
