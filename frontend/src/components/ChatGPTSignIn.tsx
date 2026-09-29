import { useEffect, useRef, useState } from 'react'
import {
  pollChatGPTLogin,
  signOutChatGPT,
  startChatGPTLogin,
  type AIProvider,
  type ChatGPTDeviceLogin,
} from '../lib/api/providers'
import { t, tNode } from '../i18n'
import { Button, fieldHintClassName } from './ui'

/**
 * The sign-in panel for a chatgpt provider, in place of the API key field. It
 * lives on the saved provider because the flow needs a provider id to store the
 * token against. The device-code flow is what makes this work on a server:
 * nothing has to reach a loopback port on the host Lemmary runs on.
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
  // the timer down several times a second.
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
    // One poll at a time: a round trip slower than the interval would put two in
    // the air over the same single-use authorization code.
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
            setError(t('chatGPTSignIn.expired'))
            return
          }
          await onChangeRef.current()
        } catch (err) {
          if (cancelled) return
          setLogin(null)
          setError(err instanceof Error ? err.message : t('chatGPTSignIn.failed'))
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
      setError(err instanceof Error ? err.message : t('chatGPTSignIn.startFailed'))
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
      setError(err instanceof Error ? err.message : t('chatGPTSignIn.signOutFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mt-2 flex flex-col gap-2">
      {provider.signed_in ? (
        <div className="flex flex-wrap items-center gap-2">
          <p className={fieldHintClassName}>
            {t(
              provider.account
                ? provider.plan
                  ? 'chatGPTSignIn.signedInAsPlan'
                  : 'chatGPTSignIn.signedInAs'
                : provider.plan
                  ? 'chatGPTSignIn.signedInPlan'
                  : 'chatGPTSignIn.signedIn',
              { account: provider.account ?? '', plan: provider.plan ?? '' },
            )}
          </p>
          <Button variant="secondary" size="xs" disabled={busy} onClick={() => void onSignOut()}>
            {t('chatGPTSignIn.signOut')}
          </Button>
        </div>
      ) : login ? (
        <div className="flex flex-col gap-1">
          <p className="text-sm text-ink">
            {tNode('chatGPTSignIn.openAndEnter', {
              link: (
                <a
                  className="underline"
                  href={login.verification_url}
                  target="_blank"
                  rel="noreferrer noopener"
                >
                  {login.verification_url}
                </a>
              ),
            })}
          </p>
          <p className="font-mono text-lg tracking-widest text-ink">{login.user_code}</p>
          <p className={fieldHintClassName}>
            {t('chatGPTSignIn.waiting')}
          </p>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" size="xs" disabled={busy} onClick={() => void onStart()}>
            {busy ? t('chatGPTSignIn.starting') : t('chatGPTSignIn.signIn')}
          </Button>
          <p className={fieldHintClassName}>
            {t('chatGPTSignIn.needsDeviceCode')}
          </p>
        </div>
      )}
      {error && <p className="text-xs text-madder">{error}</p>}
    </div>
  )
}
