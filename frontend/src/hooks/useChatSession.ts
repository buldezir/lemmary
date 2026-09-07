import { useCallback, useEffect, useRef, useState } from 'react'
import { StreamInterruptedError } from '../lib/apiClient'
import {
  toChatTurn,
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
  /** Failure of the last send; cleared when the next one starts. */
  error: string
  /** Failure to load the session named in the URL. */
  loadError: string
  /** True when a turn was answered but could not be stored. */
  unsaved: boolean
  /** Why, when `unsaved` is true. */
  unsavedDetail: string
  submit: () => Promise<void>
  /** Abandons an unsaved chat and starts a fresh one in place. */
  reset: () => void
}

/**
 * Owns one conversation: its transcript, its composer, and the send.
 *
 * Not `useAsync`, for two reasons specific to a chat. It re-runs whenever its
 * deps change, and the whole trick below is that the id changing from undefined
 * to a freshly created one must *not* refetch — the turns are already here, and
 * refetching would throw away the optimistic bubble mid-send. And its stale
 * guard only orders responses; it never clears `data`, so switching from chat A
 * to chat B would keep A's transcript on screen while B loads, which reads as
 * though the message went to the wrong conversation.
 */
export function useChatSession(options: UseChatSessionOptions): UseChatSessionResult {
  const { sessionId } = options

  const [session, setSession] = useState<ChatSession | null>(null)
  const [turns, setTurns] = useState<ChatTurn[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [sending, setSending] = useState(false)
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

  // Held in refs and refreshed each render, like useAsync's loadRef, so a
  // caller can pass inline closures (which read live state such as the
  // Search/Research toggle) without them becoming effect dependencies.
  const loadRef = useRef(options.load)
  const sendRef = useRef(options.send)
  const settledRef = useRef(options.onSessionSettled)
  useEffect(() => {
    loadRef.current = options.load
    sendRef.current = options.send
    settledRef.current = options.onSessionSettled
  })

  useEffect(() => {
    const next = sessionId ?? null
    // The promotion no-op. After a send created the session, ownedRef already
    // holds its id, so this effect firing on the new URL must do nothing at
    // all — no refetch, no flicker, no race with the turn just appended.
    if (ownedRef.current === next) {
      return
    }

    const previous = ownedRef.current
    ownedRef.current = next
    const epoch = ++epochRef.current

    // The microtask keeps these setState calls out of the effect's synchronous
    // body, so switching conversations cannot cascade renders — the same shape
    // useAsync uses. A cancelled switch is caught by the epoch either way.
    let started = false
    let cancelled = false
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

      if (!next) {
        setLoading(false)
        return
      }

      setLoading(true)
      loadRef
        .current(next)
        .then((detail) => {
          if (epochRef.current !== epoch) {
            return
          }
          setSession(detail.session)
          setTurns(detail.messages.map((message) => toChatTurn(message)))
        })
        .catch((err: unknown) => {
          if (epochRef.current !== epoch) {
            return
          }
          setLoadError(err instanceof Error ? err.message : 'Failed to load the chat')
        })
        .finally(() => {
          if (epochRef.current === epoch) {
            setLoading(false)
          }
        })
    })

    return () => {
      cancelled = true
      // Hand the claim back when the load never got as far as starting.
      //
      // React invokes an effect twice on mount in development, and the teardown
      // in between cancels the queued load. Without this the second run would
      // find the id already claimed, take the promotion no-op above, and a
      // conversation opened directly by its URL would never load at all --
      // visible only in a development build, since a production one mounts
      // once. Gated on `started` because the promotion itself relies on a claim
      // outliving its effect: the send sets ownedRef before navigating, and the
      // teardown that follows must not undo it.
      if (!started && ownedRef.current === next) {
        ownedRef.current = previous
      }
    }
  }, [sessionId])

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
    setTurns((current) => [...current, pending])

    try {
      const result = await sendRef.current({ sessionId: owner ?? undefined, content: text })
      if (epochRef.current !== epoch) {
        // The user moved to another conversation while this was in flight. The
        // turn is stored server-side; dropping it here just avoids pasting it
        // into a chat it does not belong to.
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
      // The question goes back in the composer so it is not lost -- except
      // when it is already being answered. A stream that broke after the run
      // started leaves the server working on this exact question, and handing
      // it back invites the user to submit it a second time and pay for the
      // same run twice. The error text tells them where the answer will be.
      if (!(err instanceof StreamInterruptedError)) {
        setInput(text)
      }
    } finally {
      // Unconditional: the request is over whichever conversation is on screen.
      // Gating this on the epoch strands the spinner forever when the user
      // switches chats mid-send.
      setSending(false)
    }
  }, [input, sending])

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
  }, [])

  return {
    session,
    turns,
    input,
    setInput,
    loading,
    sending,
    error,
    loadError,
    unsaved,
    unsavedDetail,
    submit,
    reset,
  }
}
