import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'

import { Button } from '../components/ui'
import { ChatPanel } from '../components/ChatPanel'
import { ChatTranscript } from '../components/ChatTranscript'
import { ChatComposer } from '../components/ChatComposer'
import { ChatWorkspaceFrame, modeHints } from '../components/ChatWorkspaceFrame'
import { MarkdownContent } from '../components/MarkdownContent'
import { BindingOverride } from '../components/BindingOverride'
import { WebSearchToggle } from '../components/WebSearchToggle'
import { useChatSession } from '../hooks/useChatSession'
import { useChatWorkspace } from '../hooks/useChatWorkspace'
import { cancelSearchRun, type ResearchEvent } from '../lib/api/ai'
import { chatSessionBinding, getChatSession } from '../lib/api/chats'
import { applyStep, type ResearchStep } from '../lib/researchSteps'
import { formatContextUsage, type ContextUsage } from '../lib/contextUsage'

/**
 * Research: the agent loop, and the page that keeps its conversation. It shows
 * the steps as they happen, what the turn took of the model's context, and --
 * because the work is stored as it is done rather than when it finishes -- it
 * can pick a turn back up where a failed run left it.
 *
 * Search is the other page. The two share the frame and the stream plumbing and
 * nothing else; everything on this page is about a turn that is more than one
 * completion.
 */
export function ResearchPage() {
  const navigate = useNavigate()
  const ws = useChatWorkspace({ mode: 'research', basePath: '/rag/research' })

  // Per turn, not per conversation: the server stores nothing about it, so a
  // reload starts from off. Off is the safe direction for a metered tool.
  const [web, setWeb] = useState(false)
  const [steps, setSteps] = useState<ResearchStep[]>([])
  const [draft, setDraft] = useState('')
  const [liveUsage, setLiveUsage] = useState<ContextUsage | null>(null)
  // Which button started the send in flight. A ref rather than state: `submit`
  // reads the send closure through a ref refreshed in an effect, so a setState
  // in the click handler would not be visible to the send that click triggers.
  const forkRef = useRef(false)
  const resumeRef = useRef(false)

  // The live view of a run, rebuilt from the frames as they arrive. Collected
  // outside React state as well, because the fold is incremental.
  const collected = useRef<ResearchStep[]>([])
  const onEvent = useCallback((event: ResearchEvent) => {
    switch (event.type) {
      case 'step':
        applyStep(collected.current, event)
        setSteps([...collected.current])
        break
      case 'delta':
        setDraft((current) => current + event.content)
        break
      case 'message':
        setDraft(event.content)
        break
      case 'usage':
        setLiveUsage({
          peak_prompt: event.prompt_tokens,
          context_window: event.context_window,
          estimated: event.estimated,
        })
        break
      default:
        break
    }
  }, [])

  const runResearch = useCallback(
    async (id: string | undefined, content: string, forkFrom?: string, resume?: boolean) => {
      collected.current = []
      setSteps([])
      setDraft('')
      setLiveUsage(null)
      try {
        return await ws.runTurn(
          { sessionId: id, content, forkFrom, resume, web, binding: ws.binding },
          onEvent,
        )
      } finally {
        setSteps([])
        setDraft('')
        setLiveUsage(null)
      }
    },
    [onEvent, web, ws],
  )

  const chat = useChatSession({
    sessionId: ws.sessionId,
    load: async (id) => {
      const detail = await getChatSession(id)
      if (detail.session.kind !== 'search') {
        throw new Error('That chat belongs to a different page.')
      }
      return detail
    },
    send: ({ sessionId: id, content }) => {
      const fork = forkRef.current && Boolean(id)
      const resume = resumeRef.current && Boolean(id)
      forkRef.current = false
      resumeRef.current = false
      if (fork) {
        return runResearch(undefined, content, id)
      }
      return runResearch(id, content, undefined, resume)
    },
    onSessionSettled: ws.onSessionSettled,
  })
  useEffect(() => {
    ws.adoptRef.current = chat.adoptSession
  }, [chat.adoptSession, ws.adoptRef])

  const loadedMode = chat.session?.mode
  useEffect(() => {
    if (!ws.sessionId || (loadedMode && loadedMode !== 'search')) {
      return
    }
    if (loadedMode === 'search') {
      void navigate({
        to: '/rag/search/$sessionId',
        params: { sessionId: ws.sessionId },
        replace: true,
      })
    }
  }, [loadedMode, navigate, ws.sessionId])

  const locked = Boolean(ws.sessionId) || chat.sending
  const inConversation = Boolean(ws.sessionId) || Boolean(chat.session)
  const shownBinding = inConversation ? chatSessionBinding(chat.session) : ws.binding
  // A fork exists to keep a transcript worth keeping, so there is nothing to
  // branch before the conversation is saved.
  const canFork = Boolean(ws.sessionId)

  function startNewChat() {
    ws.setRailOpen(false)
    ws.endRun()
    if (ws.sessionId) {
      void navigate({ to: '/rag/research' })
      return
    }
    chat.reset()
  }

  /**
   * Picks the last turn back up. Its question, its searches and everything it
   * read are already stored, so this re-enters the loop where the run stopped
   * rather than paying for that work twice.
   */
  function continueTurn() {
    resumeRef.current = true
    chat.setUnfinished(false)
    void chat.submit({ resume: true })
  }

  return (
    <ChatWorkspaceFrame
      mode="research"
      hint={modeHints.research}
      locked={locked}
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
      {ws.forkError && <p className="mb-3 text-sm text-madder">{ws.forkError}</p>}
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
          sendingLabel="Researching..."
          emptyHint={'Try something like: "how much did I spend on the car in 2024?"'}
          renderBefore={(turn) =>
            turn.steps && turn.steps.length > 0 ? <StepList steps={turn.steps} collapsed /> : null
          }
          renderExtra={(turn) => (
            <>
              {turn.incomplete && <IncompleteNotice />}
              {turn.usage && <ContextUsageNotice usage={turn.usage} />}
              {/* Only on a stored answer: an id-less bubble is one the server
                  could not save, and a copy taken at it would end on the turn
                  before instead. */}
              {canFork && turn.role === 'assistant' && Boolean(turn.id) && (
                <Button
                  variant="secondary"
                  size="xs"
                  title="Copy this chat up to this answer and continue there, leaving this one as it is."
                  disabled={chat.sending || ws.railBusy}
                  onClick={() => void ws.onForkFrom(turn.id)}
                >
                  ⑂ Fork from here
                </Button>
              )}
            </>
          )}
          renderSending={() => (
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
          )}
        />
        {/* Above the composer, because it is about the turn that is already
            there rather than the next one. */}
        {chat.unfinished && !chat.sending && (
          <UnfinishedNotice onContinue={continueTurn} disabled={chat.loading || ws.railBusy} />
        )}
        <ChatComposer
          value={chat.input}
          onChange={chat.setInput}
          onSubmit={() => void chat.submit()}
          placeholder="Ask a question about your documents..."
          submitLabel="Research"
          sendingLabel="Researching..."
          sending={chat.sending}
          disabled={chat.loading}
          error={chat.error}
          onCancel={
            chat.resuming && ws.sessionId
              ? () => void cancelSearchRun({ sessionId: ws.sessionId as string })
              : ws.endRun
          }
          secondary={
            ws.sessionId
              ? {
                  label: 'Research in fork',
                  title: 'Ask this in a copy of the chat, leaving this one as it is.',
                  onClick: () => {
                    forkRef.current = true
                    void chat.submit()
                  },
                }
              : undefined
          }
          autoFocus
        />
      </ChatPanel>
      <div className="border border-t-0 border-line bg-surface px-4 py-3">
        <div className="mb-3">
          <WebSearchToggle checked={web} onChange={setWeb} disabled={chat.sending} />
        </div>
        <BindingOverride
          label="Search"
          purpose="llm"
          value={shownBinding}
          onChange={ws.setBinding}
          locked={inConversation || chat.sending || chat.turns.length > 0}
          lockedHint="Fixed for this chat. Start a new one to research with a different model."
          help="Drives the search or research loop. The helper model that reads documents in bulk keeps its own binding in Settings."
          showConfigured
        />
      </div>
    </ChatWorkspaceFrame>
  )
}

/**
 * A turn whose run stopped before it answered. What it got through is stored,
 * which is what makes continuing cheaper than asking again.
 */
function UnfinishedNotice({ onContinue, disabled }: { onContinue: () => void; disabled: boolean }) {
  return (
    <div className="flex flex-wrap items-center gap-3 border-t border-line bg-paper px-4 py-3">
      <p className="text-sm text-ink-muted">
        This question was not finished. The research it had done is kept.
      </p>
      <Button variant="secondary" size="xs" disabled={disabled} onClick={onContinue}>
        Continue
      </Button>
    </div>
  )
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
