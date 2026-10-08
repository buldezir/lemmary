import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'

import { Button, DocsLink } from '../components/ui'
import { ChatPanel } from '../components/ChatPanel'
import { ChatTranscript } from '../components/ChatTranscript'
import { ChatComposer } from '../components/ChatComposer'
import { ChatWorkspaceFrame } from '../components/ChatWorkspaceFrame'
import { MarkdownContent } from '../components/MarkdownContent'
import { BindingOverride } from '../components/BindingOverride'
import { DocumentFilters } from '../components/DocumentFilters'
import { WebSearchToggle } from '../components/WebSearchToggle'
import { useChatSession } from '../hooks/useChatSession'
import { useChatWorkspace } from '../hooks/useChatWorkspace'
import { useDocumentFilterOptions } from '../hooks/useDocumentList'
import { cancelSearchRun, type ResearchEvent } from '../lib/api/ai'
import { chatSessionBinding, getChatSession } from '../lib/api/chats'
import {
  countMatchingDocuments,
  listMatchingDocumentIds,
  type DocumentListFilters,
} from '../lib/api/documents'
import {
  defaultDocumentQuery,
  documentQuerySearch,
  hasActiveFilters,
  parseDocumentQuery,
  searchableTerm,
  tagIds,
  type DocumentQuery,
} from '../lib/documentQuery'
import { applyStep, type ResearchStep } from '../lib/researchSteps'
import {
  contextOverflowWarning,
  formatContextUsage,
  type ContextUsage,
} from '../lib/contextUsage'
import { docsUrl, t, tNode } from '../i18n'

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
  // The documents a turn may reach, picked with the Documents list's filters.
  // Every turn sends them and the chat keeps the last, so opening a chat puts
  // its filters back; a new chat starts with none.
  const [scope, setScope] = useState<DocumentQuery>(defaultDocumentQuery)
  const [scopeSearch, setScopeSearch] = useState('')
  const [scopeRestoredFor, setScopeRestoredFor] = useState<string | undefined>()
  const [scopeOpen, setScopeOpen] = useState(false)
  const scopeTerm = searchableTerm(scopeSearch)
  const scoped = hasActiveFilters({ ...scope, q: scopeTerm })
  const [scopeCount, setScopeCount] = useScopeCount(scope, scopeTerm, scoped)
  const [steps, setSteps] = useState<ResearchStep[]>([])
  const [draft, setDraft] = useState('')
  const [liveUsage, setLiveUsage] = useState<ContextUsage | null>(null)
  // Whether the send in flight is a resume. A ref rather than state: `submit`
  // reads the send closure through a ref refreshed in an effect, so a setState
  // in the click handler would not be visible to the send that click triggers.
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
    async (id: string | undefined, content: string, resume?: boolean) => {
      collected.current = []
      setSteps([])
      setDraft('')
      setLiveUsage(null)
      try {
        const ids = scoped ? await scopeIds(scope, scopeTerm, scopeCount) : undefined
        if (ids) setScopeCount(ids.length)
        return await ws.runTurn(
          {
            sessionId: id,
            content,
            resume,
            web,
            binding: ws.binding,
            scope: ids,
            filters: documentQuerySearch({ ...scope, q: scopeTerm }),
          },
          onEvent,
        )
      } finally {
        setSteps([])
        setDraft('')
        setLiveUsage(null)
      }
    },
    [onEvent, web, ws, scoped, scopeTerm, scope, scopeCount, setScopeCount],
  )

  const chat = useChatSession({
    sessionId: ws.sessionId,
    load: async (id) => {
      const detail = await getChatSession(id)
      if (detail.session.kind !== 'search') {
        throw new Error(t('search.wrongPage'))
      }
      return detail
    },
    send: ({ sessionId: id, content }) => {
      const resume = resumeRef.current && Boolean(id)
      resumeRef.current = false
      return runResearch(id, content, resume)
    },
    onSessionSettled: ws.onSessionSettled,
  })
  const { setAdopt } = ws
  useEffect(() => setAdopt(chat.adoptSession), [chat.adoptSession, setAdopt])

  // Adjusted during render rather than in an effect, once per chat opened: the
  // one in the URL, once it has loaded, so a chat still loading does not read
  // as a new one. A new chat has none, and so no filters. Unfolded when there
  // are some, so a chat says what it is limited to.
  const wantedId = ws.sessionId
  const loaded = !wantedId || chat.session?.id === wantedId
  if (loaded && wantedId !== scopeRestoredFor) {
    setScopeRestoredFor(wantedId)
    const saved = parseDocumentQuery((wantedId && chat.session?.filters) || {})
    setScope({ ...saved, q: '' })
    setScopeSearch(saved.q)
    setScopeOpen(hasActiveFilters(saved))
  }

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

  const inConversation = Boolean(ws.sessionId) || Boolean(chat.session)
  const shownBinding = inConversation ? chatSessionBinding(chat.session) : ws.binding
  // A fork exists to keep a transcript worth keeping, so there is nothing to
  // branch before the conversation is saved.
  const canFork = Boolean(ws.sessionId)

  // The last turn that reported one: only a model whose window the catalogue
  // knows reports it at all, and nothing here knows it before the first answer.
  const knownUsage = chat.turns.findLast((turn) => turn.usage)?.usage
  const overflow = chat.sending ? '' : contextOverflowWarning(chat.input, knownUsage)

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
      title={t('research.title')}
      hint={tNode('research.hint', {
        link: <DocsLink href={docsUrl('deep_research.html')}>{t('research.howItWorks')}</DocsLink>,
      })}
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
          {chat.unsavedDetail || t('search.unsaved')}
        </p>
      )}
      <ChatPanel>
        <ChatTranscript
          conversationId={ws.sessionId}
          turns={chat.turns}
          loading={chat.loading}
          sending={chat.sending}
          sendingLabel={t('research.researching')}
          emptyHint={t('research.emptyHint')}
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
                  title={t('research.forkTitle')}
                  disabled={chat.sending || ws.railBusy}
                  onClick={() => void ws.onForkFrom(turn.id)}
                >
                  ⑂ {t('research.fork')}
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
        {overflow && (
          <p className="border-t border-line bg-paper px-4 pt-3 text-xs text-amber-800">
            {overflow}
          </p>
        )}
        <ChatComposer
          value={chat.input}
          onChange={chat.setInput}
          onSubmit={() => void chat.submit()}
          placeholder={t('research.placeholder')}
          submitLabel={t('research.research')}
          sendingLabel={t('research.researching')}
          sending={chat.sending}
          disabled={chat.loading}
          error={chat.error}
          onCancel={
            chat.resuming && ws.sessionId
              ? () => void cancelSearchRun({ sessionId: ws.sessionId as string })
              : ws.endRun
          }
          autoFocus
        />
      </ChatPanel>
      <div className="border border-t-0 border-line bg-surface px-4 py-3">
        <ScopeFilters
          query={scope}
          search={scopeSearch}
          onSearchChange={setScopeSearch}
          updateQuery={(patch) => setScope((current) => ({ ...current, ...patch }))}
          summary={
            !scoped
              ? t('research.scopeAll')
              : scopeCount === null
                ? t('research.scopeCounting')
                : scopeCount === 'failed'
                  ? t('research.scopeUnknown')
                  : t('research.scopeCount', { count: scopeCount })
          }
          disabled={chat.sending || !loaded}
          open={scopeOpen}
          onOpenChange={setScopeOpen}
        />
        <div className="mb-3">
          <WebSearchToggle checked={web} onChange={setWeb} disabled={chat.sending} />
        </div>
        <BindingOverride
          label={t('research.research')}
          purpose="llm"
          bindingName="research"
          value={shownBinding}
          onChange={ws.setBinding}
          locked={inConversation || chat.sending || chat.turns.length > 0}
          lockedHint={t('research.lockedHint')}
          help={t('research.bindingHelp')}
          showConfigured
        />
      </div>
    </ChatWorkspaceFrame>
  )
}

function listFilters(query: DocumentQuery): DocumentListFilters {
  return {
    status: query.status,
    documentType: query.type,
    correspondent: query.correspondent,
    dateFrom: query.from,
    dateTo: query.to,
    undated: query.undated,
    tags: tagIds(query.tags),
    untagged: query.untagged,
    owner: query.owner,
  }
}

// Mirrors the server's maxScopeDocuments, so a scope past it is refused before
// its ids are collected and sent.
const maxScopeDocuments = 10000

type ScopeCount = number | 'failed'

/**
 * The ids a scoped turn is sent with. A count already in hand refuses an empty
 * or too wide scope without a request; the collection itself stops at the cap.
 */
async function scopeIds(
  query: DocumentQuery,
  term: string,
  count: ScopeCount | null,
): Promise<string[]> {
  if (count === 0) throw new Error(t('research.scopeEmpty'))
  if (typeof count === 'number' && count > maxScopeDocuments) {
    throw new Error(t('documents.tooManyMatches', { count: maxScopeDocuments }))
  }
  const ids = await listMatchingDocumentIds(term, listFilters(query), maxScopeDocuments)
  if (ids.length === 0) throw new Error(t('research.scopeEmpty'))
  return ids
}

/**
 * How many documents the scope holds: null until the count for these filters
 * is in, 'failed' when it could not be had. The setter settles it from a send,
 * whose ids are the one ranking the model is told the size of.
 */
function useScopeCount(
  query: DocumentQuery,
  term: string,
  scoped: boolean,
): [ScopeCount | null, (count: number) => void] {
  const key = JSON.stringify({ ...query, q: term })
  const [counted, setCounted] = useState<{ key: string; count: ScopeCount } | null>(null)
  useEffect(() => {
    if (!scoped) return
    let live = true
    // Debounced like the Documents list's search box: a change per keystroke.
    const timer = window.setTimeout(() => {
      countMatchingDocuments(term, listFilters(query)).then(
        (count) => live && setCounted({ key, count }),
        () => live && setCounted({ key, count: 'failed' }),
      )
    }, 300)
    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [query, term, scoped, key])
  const settle = useCallback((count: number) => setCounted({ key, count }), [key])
  return [counted?.key === key ? counted.count : null, settle]
}

/** The Bulk actions filters, folded away, saying only how many documents they keep. */
function ScopeFilters({
  query,
  search,
  onSearchChange,
  updateQuery,
  summary,
  disabled,
  open,
  onOpenChange,
}: {
  query: DocumentQuery
  search: string
  onSearchChange: (value: string) => void
  updateQuery: (patch: Partial<DocumentQuery>) => void
  summary: string
  disabled: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const options = useDocumentFilterOptions()
  return (
    <details
      className="mb-3"
      open={open}
      onToggle={(event) => onOpenChange(event.currentTarget.open)}
    >
      <summary className="cursor-pointer text-sm text-ink">{summary}</summary>
      <fieldset disabled={disabled} className="mt-3 min-w-0">
        <DocumentFilters
          query={query}
          search={search}
          onSearchChange={onSearchChange}
          updateQuery={updateQuery}
          documentTypes={options.documentTypes}
          correspondents={options.correspondents}
          tags={options.tags}
          status={query.status}
        />
        {options.error && <p className="mt-2 text-sm text-madder">{options.error}</p>}
      </fieldset>
    </details>
  )
}

/**
 * A turn whose run stopped before it answered. What it got through is stored,
 * which is what makes continuing cheaper than asking again.
 */
function UnfinishedNotice({ onContinue, disabled }: { onContinue: () => void; disabled: boolean }) {
  return (
    <div className="flex flex-wrap items-center gap-3 border-t border-line bg-paper px-4 py-3">
      <p className="text-sm text-ink-muted">{t('research.unfinished')}</p>
      <Button variant="secondary" size="xs" disabled={disabled} onClick={onContinue}>
        {t('research.continue')}
      </Button>
    </div>
  )
}

function IncompleteNotice() {
  return (
    <p className="border-t border-line pt-2 text-xs text-ink-muted">{t('research.incomplete')}</p>
  )
}

function ContextUsageNotice({ usage }: { usage: ContextUsage }) {
  const text = formatContextUsage(usage)
  if (!text) return null
  return <p className="border-t border-line pt-2 text-xs text-ink-muted">
      {t('research.contextUsed', { usage: text })}
    </p>
}

function LiveContextUsage({ usage }: { usage: ContextUsage }) {
  const text = formatContextUsage(usage)
  if (!text) return null
  return (
    <p className="border-l-2 border-line pl-3 text-xs text-ink-faint tabular-nums">
      {t('research.contextLive', { usage: text })}
    </p>
  )
}

function StepList({ steps, collapsed = false }: { steps: ResearchStep[]; collapsed?: boolean }) {
  if (steps.length === 0) {
    return (
      <p className="text-xs text-ink-faint">
        <span className="animate-pulse">{t('research.researchingArchive')}</span>
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
        {t('research.steps', { count: steps.length })}
      </summary>
      <div className="mt-1">{list}</div>
    </details>
  )
}
