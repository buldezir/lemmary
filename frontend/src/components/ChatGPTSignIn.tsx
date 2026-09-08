import { useEffect, useRef, useState } from 'react'
import {
  pollChatGPTLogin,
  signOutChatGPT,
  startChatGPTLogin,
  type AIProvider,
  type ChatGPTDeviceLogin,
} from '../lib/api/providers'
import { Button, fieldHintClassName } from './ui'

/**
 * The sign-in panel for a chatgpt provider, in place of the API key field every
 * other SDK shows.
 *
 * It lives on the saved provider rather than in the add form because the flow
 * needs a provider id to store the token against: an operator creates the row
 * first, then signs in to it. That also makes signing in as a different account
 * the same two clicks as signing in the first time.
 *
 * The device-code flow is what makes this work on a server: the code is typed
 * into a browser on any machine, so nothing has to reach a loopback port on the
 * host Lemmary runs on.
 */
export function ChatGPTSignIn({
  provider,
  onChange,
}: {
  provider: AIProvider
  // Widened to Promise<unknown> so the page can pass its own provider reload,
  // which resolves to the new list rather than to nothing.
  onChange: () => void | Promise<unknown>
}) {
  const [login, setLogin] = useState<ChatGPTDeviceLogin | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  // Held in a ref so the polling effect does not list it as a dependency: the
  // page passes a fresh closure on every render, and depending on it would tear
  // the timer down and start a new interval several times a second.
  const onChangeRef = useRef(onChange)
  useEffect(() => {
    onChangeRef.current = onChange
  })

  useEffect(() => {
    if (!login) return
    let cancelled = false

    // The server's own interval, not one of our choosing: polling faster than
    // OpenAI asks for is what earns a slow_down.
    const period = Math.max(login.interval_seconds, 1) * 1000
    // One poll at a time. A round trip slower than the interval would otherwise
    // have two in the air asking about the same single-use authorization code,
    // and whichever settled last would decide what the operator sees.
    let inFlight = false
    const timer = setInterval(() => {
      if (inFlight) return
      inFlight = true
      void (async () => {
        try {
          const result = await pollChatGPTLogin(provider.id)
          if (cancelled) return
          if (result.status === 'pending') return
          setLogin(null)
          if (result.status === 'expired') {
            setError('That code expired. Start again to get a new one.')
            return
          }
          await onChangeRef.current()
        } catch (err) {
          if (cancelled) return
          setLogin(null)
          setError(err instanceof Error ? err.message : 'ChatGPT sign-in failed')
        } finally {
          inFlight = false
        }
      })()
    }, period)

    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [login, provider.id])

  async function onStart() {
    setBusy(true)
    setError('')
    try {
      setLogin(await startChatGPTLogin(provider.id))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start the ChatGPT sign-in')
    } finally {
      setBusy(false)
    }
  }

  async function onSignOut() {
    setBusy(true)
    setError('')
    try {
      await signOutChatGPT(provider.id)
      setLogin(null)
      await onChange()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to sign out')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mt-2 flex flex-col gap-2">
      {provider.signed_in ? (
        <div className="flex flex-wrap items-center gap-2">
          <p className={fieldHintClassName}>
            Signed in{provider.account ? ` as ${provider.account}` : ''}
            {provider.plan ? ` (${provider.plan})` : ''}.
          </p>
          <Button variant="secondary" size="xs" disabled={busy} onClick={() => void onSignOut()}>
            Sign out
          </Button>
        </div>
      ) : login ? (
        <div className="flex flex-col gap-1">
          <p className="text-sm text-ink">
            Open{' '}
            <a
              className="underline"
              href={login.verification_url}
              target="_blank"
              rel="noreferrer noopener"
            >
              {login.verification_url}
            </a>{' '}
            and enter this code:
          </p>
          <p className="font-mono text-lg tracking-widest text-ink">{login.user_code}</p>
          <p className={fieldHintClassName}>
            Waiting for you to approve it. The code is good for about fifteen minutes.
          </p>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" size="xs" disabled={busy} onClick={() => void onStart()}>
            {busy ? 'Starting…' : 'Sign in with ChatGPT'}
          </Button>
          <p className={fieldHintClassName}>
            Needs device code sign-in enabled on the account, under ChatGPT Settings → Security.
          </p>
        </div>
      )}
      {error && <p className="text-xs text-madder">{error}</p>}
    </div>
  )
}
