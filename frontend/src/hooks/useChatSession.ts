import { useCallback, useEffect, useRef, useState } from 'react'
import { RunInFlightError } from '../lib/apiClient'
import {
  toChatTurn,
  waitWhileRunning,
  type ChatMessageRecord,
  type ChatSession,
  type ChatSessionDetail,
  type ChatTurn,
  type SearchDocumentHit,
} from '../lib/api/chats'

export type ChatSendResult = {
  session: ChatSession | null
  message: ChatMessageRecord
  documents?: SearchDocumentHit[]
  saved: boolean
  /** Why the turn could not be stored, when `saved` is false. */
  detail?: string
}

export type UseChatSessionOptions = {
  /** The session in the URL; undefined is an unsaved new chat. */
  sessionId?: string
  load: (id: string) => Promise<ChatSessionDetail>
  send: (input: { sessionId?: string; content: string }) => Promise<ChatSendResult>
  /** Called after a successful turn. `created` is true when this send is what brought the session into being. */
  onSessionSettled?: (session: ChatSession, created: boolean) => void
}

export type UseChatSessionResult = {
  session: ChatSession | null
  turns: ChatTurn[]
  input: string
  setInput: (value: string) => void
  loading: boolean
  sending: boolean
  /** True when this page is observing a run started before it mounted. */
  resuming: boolean
  /** Failure of the last send; cleared when the next one starts. */
  error: string
  /** Failure to load the session named in the URL. */
  loadError: string
  /** True when a turn was answered but could not be stored. */
  unsaved: boolean
  unsavedDetail: string
  submit: () => Promise<void>
  /** Abandons an unsaved chat and starts a fresh one in place. */
  reset: () => void
  /**
   * Moves the conversation in flight into another session, for a send the
   * server answers in a conversation of its own making. Claim it before the URL
   * follows: the load effect reads a claimed id as a promotion and leaves the
   * running turn alone, where an unclaimed one is a switch that throws the
   * transcript and the reply being watched away.
   */
  adoptSession: (session: ChatSession) => void
}

/**
 * Not `useAsync`: the id changing from undefined to a freshly created one must
 * *not* refetch, or the optimistic bubble is thrown away mid-send; and
 * useAsync never clears `data`, so chat A's transcript would stay on screen
 * while chat B loads.
 */
export function useChatSession(options: UseChatSessionOptions): UseChatSessionResult {
  const { sessionId } = options

  const [session, setSession] = useState<ChatSession | null>(null)
  const [turns, setTurns] = useState<ChatTurn[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [sending, setSending] = useState(false)
  const [resuming, setResuming] = useState(false)
  const [error, setError] = useState('')
  const [loadError, setLoadError] = useState('')
  const [unsaved, setUnsaved] = useState(false)
  const [unsavedDetail, setUnsavedDetail] = useState('')

  // The session id whose turns are currently in state; null for an unsaved
  // chat. Claimed synchronously on send, before the caller navigates.
  const ownedRef = useRef<string | null>(null)
  // Bumped on every genuine conversation switch. An id comparison is not
  // enough: "New chat" during a send on an unsaved chat leaves the owner null
  // and the new chat null too, and the in-flight reply would land in the fresh
  // conversation.
  const epochRef = useRef(0)
  const pendingIdRef = useRef(0)

  // Refreshed each render, like useAsync's loadRef, so a caller can pass
  // inline closures without them becoming effect dependencies.
  const loadRef = useRef(options.load)
  const sendRef = useRef(options.send)
  const settledRef = useRef(options.onSessionSettled)
  useEffect(() => {
    loadRef.current = options.load
    sendRef.current = options.send
    settledRef.current = options.onSessionSettled
  })

  /**
   * Sits out a run this page did not start, then shows what it produced. A run
   * outlives the connection by design, and its turn is only stored when it
   * ends, so `sending` is what puts a reloaded tab back into its waiting state.
   */
  const resume = useCallback(
    (id: string, known: number, epoch: number, signal: AbortSignal) => {
      setSending(true)
      setResuming(true)
      void waitWhileRunning(id, { signal })
        .then((settled) => {
          if (epochRef.current !== epoch) {
            return
          }
          if (!settled || settled.messages.length <= known) {
            // The run ended and stored nothing: it failed, or someone cancelled
            // it from the tab that started it.
            setError('That run ended without an answer.')
            return
          }
          setSession(settled.session)
          setTurns(settled.messages.map((message) => toChatTurn(message)))
          settledRef.current?.(settled.session, false)
        })
        .catch((err: unknown) => {
          if (epochRef.current === epoch && !signal.aborted) {
            setError(err instanceof Error ? err.message : 'Failed to follow the running chat')
          }
        })
        .finally(() => {
          if (epochRef.current === epoch) {
            setSending(false)
            setResuming(false)
          }
        })
    },
    [],
  )

  useEffect(() => {
    const next = sessionId ?? null
    // The promotion no-op: after a send created the session ownedRef already
    // holds its id, so firing on the new URL must not refetch.
    if (ownedRef.current === next) {
      return
    }

    const previous = ownedRef.current
    ownedRef.current = next
    const epoch = ++epochRef.current

    // The microtask keeps these setState calls out of the effect's synchronous
    // body, so switching conversations cannot cascade renders, as in useAsync.
    let started = false
    let cancelled = false
    let resumeController: AbortController | null = null
    void Promise.resolve().then(() => {
      if (cancelled || epochRef.current !== epoch) {
        return
      }
      started = true

      setSession(null)
      setTurns([])
      setInput('')
      setError('')
      setLoadError('')
      setUnsaved(false)
      setUnsavedDetail('')
      // A resumed run has no live submit promise whose finally releases the
      // flag, and sending belongs to the conversation being left.
      setSending(false)
      setResuming(false)

      if (!next) {
        setLoading(false)
        return
      }

      setLoading(true)
      loadRef
        .current(next)
        .then((detail) => {
          if (cancelled || epochRef.current !== epoch) {
            return
          }
          setSession(detail.session)
          setTurns(detail.messages.map((message) => toChatTurn(message)))
          if (detail.running) {
            resumeController = new AbortController()
            resume(next, detail.messages.length, epoch, resumeController.signal)
          }
        })
        .catch((err: unknown) => {
          if (cancelled || epochRef.current !== epoch) {
            return
          }
          setLoadError(err instanceof Error ? err.message : 'Failed to load the chat')
        })
        .finally(() => {
          if (!cancelled && epochRef.current === epoch) {
            setLoading(false)
          }
        })
    })

    return () => {
      cancelled = true
      resumeController?.abort()
      // Hand the claim back when the load never got as far as starting: the
      // development double-mount cancels the queued load, and the second run
      // would otherwise take the promotion no-op and never load at all. Gated
      // on `started` because the send's claim must outlive its own teardown.
      if (!started && ownedRef.current === next) {
        ownedRef.current = previous
      }
    }
    // resume has no deps of its own, so it never re-runs this effect; listed
    // only for the linter.
  }, [sessionId, resume])

  const submit = useCallback(async () => {
    const text = input.trim()
    if (!text || sending) {
      return
    }

    const epoch = epochRef.current
    const owner = ownedRef.current
    const pending: ChatTurn = {
      id: `pending-${++pendingIdRef.current}`,
      role: 'user',
      content: text,
    }

    setSending(true)
    setInput('')
    setError('')
    setUnsaved(false)
    setUnsavedDetail('')
    setResuming(false)
    setTurns((current) => [...current, pending])

    try {
      const result = await sendRef.current({ sessionId: owner ?? undefined, content: text })
      if (epochRef.current !== epoch) {
        // Moved to another conversation mid-flight. The turn is stored
        // server-side; dropping it avoids pasting it into the wrong chat.
        return
      }

      if (result.session) {
        // Claimed before onSessionSettled navigates, so the load effect's
        // ownership check short-circuits on the new URL.
        ownedRef.current = result.session.id
        setSession(result.session)
      }
      setTurns((current) => [...current, toChatTurn(result.message, result.documents)])
      setUnsaved(!result.saved)
      setUnsavedDetail(result.saved ? '' : (result.detail ?? ''))

      if (result.session) {
        settledRef.current?.(result.session, owner === null)
      }
    } catch (err: unknown) {
      if (epochRef.current !== epoch) {
        return
      }
      setError(err instanceof Error ? err.message : 'Failed to get AI response')
      setTurns((current) => current.filter((turn) => turn.id !== pending.id))
      // The question goes back in the composer unless it is already being
      // answered: a broken stream leaves the server working on it, and
      // resubmitting would pay for the same run twice.
      if (!(err instanceof RunInFlightError)) {
        setInput(text)
      }
    } finally {
      // Unconditional: gating on the epoch strands the spinner forever when
      // the user switches chats mid-send.
      setSending(false)
    }
  }, [input, sending])

  const adoptSession = useCallback((next: ChatSession) => {
    ownedRef.current = next.id
    setSession(next)
  }, [])

  const reset = useCallback(() => {
    ownedRef.current = null
    epochRef.current += 1
    setSession(null)
    setTurns([])
    setInput('')
    setError('')
    setLoadError('')
    setUnsaved(false)
    setUnsavedDetail('')
    setLoading(false)
    setSending(false)
    setResuming(false)
  }, [])

  return {
    session,
    turns,
    input,
    setInput,
    loading,
    sending,
    resuming,
    error,
    loadError,
    unsaved,
    unsavedDetail,
    submit,
    reset,
    adoptSession,
  }
}
