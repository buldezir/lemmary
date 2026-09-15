import { useCallback, useEffect, useRef, useState } from 'react'
import { useMatchRoute, useNavigate } from '@tanstack/react-router'

import { runId } from '../lib/runId'
import { RunInFlightError } from '../lib/apiClient'
import { useAsync } from './useAsync'
import type { ChatSendResult } from './useChatSession'
import type { ProviderBinding } from '../lib/api/providers'
import { cancelSearchRun, searchStream, type ResearchEvent, type SearchMode } from '../lib/api/ai'
import {
  deleteChatSession,
  forkChatSession,
  listChatSessions,
  mergeChatSession,
  renameChatSession,
  waitForStoredTurn,
  type ChatSession,
} from '../lib/api/chats'

export type WorkspaceBasePath = '/rag/search' | '/rag/research'

/**
 * Everything the two chat pages share: the rail and its sessions, the
 * conversation in the URL, the model binding, and the machinery of running one
 * turn as a stream.
 *
 * What it deliberately does not know is what a turn *is*. Search answers once
 * from a list of hits; research keeps a conversation, reports its steps and can
 * be continued where it stopped. Those live in the pages, which is why they are
 * two pages: the run is where they differ, and this is the part that is the
 * same either way.
 */
export function useChatWorkspace({
  mode,
  basePath,
}: {
  mode: SearchMode
  basePath: WorkspaceBasePath
}) {
  const navigate = useNavigate()
  // The session id lives on a child route, so useParams cannot see it here.
  // matchRoute also hands back a fresh object each render, so the id is
  // destructured out before anything depends on it.
  const matchRoute = useMatchRoute()
  const sessionMatch = matchRoute({
    to: basePath === '/rag/research' ? '/rag/research/$sessionId' : '/rag/search/$sessionId',
  })
  const sessionId = sessionMatch ? (sessionMatch.sessionId as string) : undefined

  const [railOpen, setRailOpen] = useState(false)
  const [justSettled, setJustSettled] = useState<ChatSession | null>(null)
  const [railBusy, setRailBusy] = useState(false)
  const [railError, setRailError] = useState('')
  // Its own line rather than railError: the button is in the transcript, and at
  // phone width the rail that would carry the message is collapsed away.
  const [forkError, setForkError] = useState('')
  // The model the next conversation opens on, deliberately kept across a new chat.
  const [binding, setBinding] = useState<ProviderBinding | undefined>()

  // The controller only abandons this page's view of the run; the id is what
  // stops the run itself. Both are needed, because the server keeps working
  // through a dropped connection.
  const runRef = useRef<{ controller: AbortController; id: string } | null>(null)
  // The chat hook's own claim, reached from inside a run. Assigned rather than
  // called directly because the run is defined before the hook that owns it.
  const adoptRef = useRef<(session: ChatSession) => void>(() => {})

  const sessions = useAsync(() => listChatSessions({ kind: 'search' }), [])

  // Stops this page painting the run, deliberately not the run itself: an
  // answer must not be thrown away just because nobody was watching it arrive.
  // Cancelling for real is `endRun`.
  useEffect(() => () => runRef.current?.controller.abort(), [])

  /**
   * Ends the run that owns the screen. Switching conversations does not unmount
   * the page, so without this a run keeps painting its steps over whichever
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
      // Also when the turn landed somewhere else than where it was typed, which
      // is what a fork does. replace only for `created`: Back should not land on
      // the now-orphaned empty page, but it should lead out of a fork and back
      // into the conversation it branched from.
      if (created || session.id !== sessionId) {
        void navigate({
          to: `${basePath}/$sessionId`,
          params: { sessionId: session.id },
          replace: created,
        })
      }
      // After every turn, not only the first: last_message_at moved and the row
      // has to move with it. reload() refreshes without a loading flash.
      void sessions.reload()
    },
    [basePath, navigate, sessionId, sessions],
  )

  /**
   * Runs one turn as a stream and resolves with the stored turn, so the
   * conversation hook treats it like any other send. Both pages stream:
   * research for its steps, search for the heartbeat underneath, without which
   * a slow answer is hung up on by anything with a read timeout in between.
   *
   * `onEvent` is where the pages differ -- steps, deltas and usage mean nothing
   * to a search turn -- and is called for every frame before this handles it.
   */
  const runTurn = useCallback(
    async (
      input: {
        sessionId?: string
        content: string
        binding?: ProviderBinding
        web?: boolean
        resume?: boolean
      },
      onEvent?: (event: ResearchEvent) => void,
    ): Promise<ChatSendResult> => {
      const run = { controller: new AbortController(), id: runId() }
      runRef.current = run

      // Collected outside React state as well: the finished turn is assembled
      // from these, and state updates are not readable synchronously.
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
            sessionId: input.sessionId,
            content: input.content,
            mode,
            runId: run.id,
            web: input.web === true,
            resume: input.resume === true,
            binding: input.binding,
          },
          (event) => {
            onEvent?.(event)
            switch (event.type) {
              case 'session':
                box.session = event.session
                // The conversation exists before the run makes a single
                // provider call, and this is where a new research chat moves
                // into it: the rail row, the URL and the steps below belong to
                // it from here rather than from once the answer is in. A
                // research run is long, and until the URL names the
                // conversation a reload during it lands on an empty page with
                // the answer nowhere in sight. Search stays put -- a search turn
                // that fails takes its session back with it, and the URL would
                // be left pointing at a chat that no longer exists.
                if (!input.sessionId && mode === 'research') {
                  adoptRef.current(event.session)
                  onSessionSettled(event.session, true)
                }
                break
              case 'delta':
                answer += event.content
                break
              case 'message':
                answer = event.content
                incomplete = event.incomplete ?? false
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
          throw cancelled(mode, err)
        } else if (err instanceof RunInFlightError && (box.session ?? input.sessionId)) {
          // The connection died, the run did not: the server stores the turn
          // regardless, so waiting for it turns a lost connection back into a
          // normal one.
          box.stored = await recoverTurn(
            box.session?.id ?? (input.sessionId as string),
            run.id,
            run.controller.signal,
          )
          if (!box.stored) {
            throw run.controller.signal.aborted
              ? cancelled(mode, err)
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
      }

      if (streamError) {
        throw new Error(streamError)
      }
      const stored = box.stored
      if (!stored) {
        throw new Error(
          mode === 'research'
            ? 'The research run ended without an answer.'
            : 'The search ended without an answer.',
        )
      }

      return {
        session: stored.session,
        message:
          stored.message.incomplete || !incomplete
            ? stored.message
            : { ...stored.message, incomplete: true },
        documents: stored.documents,
        saved: stored.saved,
        detail: stored.detail,
      }
    },
    [mode, onSessionSettled],
  )

  // A chat opens in the mode its last turn ran in: continuing a research
  // conversation as a plain search answers a different question than the
  // transcript above it.
  const openSession = useCallback(
    (session: ChatSession) => {
      setRailOpen(false)
      endRun()
      void navigate({
        to: session.mode === 'research' ? '/rag/research/$sessionId' : '/rag/search/$sessionId',
        params: { sessionId: session.id },
      })
    },
    [endRun, navigate],
  )

  const onRename = useCallback(
    async (id: string, title: string) => {
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
    },
    [sessions],
  )

  /**
   * Branches the conversation at one of its answers. The copy is saved before
   * this returns, so there is nothing to adopt: onSessionSettled merges the row
   * and navigates, and the hook loads the fork as the chat switch it is.
   */
  const onForkFrom = useCallback(
    async (messageId: string) => {
      if (!sessionId) {
        return
      }
      try {
        setRailBusy(true)
        setForkError('')
        onSessionSettled(await forkChatSession(sessionId, messageId), false)
      } catch (err) {
        setForkError(err instanceof Error ? err.message : 'Failed to fork the chat')
      } finally {
        setRailBusy(false)
      }
    },
    [onSessionSettled, sessionId],
  )

  const onDelete = useCallback(
    async (session: ChatSession) => {
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
    },
    [basePath, endRun, navigate, sessionId, sessions],
  )

  return {
    sessionId,
    basePath,
    navigate,
    rows: mergeChatSession(sessions.data ?? [], justSettled),
    sessions,
    railOpen,
    setRailOpen,
    railBusy,
    railError,
    forkError,
    binding,
    setBinding,
    adoptRef,
    endRun,
    runTurn,
    onSessionSettled,
    openSession,
    onRename,
    onForkFrom,
    onDelete,
  }
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
