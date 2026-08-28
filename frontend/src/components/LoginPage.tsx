import { type SubmitEvent, useEffect, useRef, useState } from 'react'
import {
  getLoginMethods,
  loginWithOAuth2,
  loginWithPasskey,
  loginWithPassword,
  startConditionalPasskeyLogin,
  type ConditionalPasskeyLogin,
  type LoginMethods,
  type OAuthProvider,
} from '../lib/auth'
import { isAbortError, passkeysSupported } from '../lib/webauthn'
import { useAsync } from '../hooks/useAsync'
import { AppFooter } from './AppFooter'
import { AppLogo, Button, inputClassName, labelClassName, labelTextClassName } from './ui'

type LoginPageProps = {
  appName: string
  accent: string
  onSuccess: () => void
}

// Shown until PocketBase reports what it accepts. Password-only matches every
// default install, so the form does not flicker in the common case.
const pendingMethods: LoginMethods = { password: true, oauth: [], passkey: false }

function MethodSeparator() {
  return (
    <div className="my-5 flex items-center gap-3" aria-hidden="true">
      <span className="h-px flex-1 bg-wash" />
      <span className="text-xs font-medium text-ink-faint">or</span>
      <span className="h-px flex-1 bg-wash" />
    </div>
  )
}

export function LoginPage({ appName, accent, onSuccess }: LoginPageProps) {
  const { data: methods } = useAsync(getLoginMethods, [])
  const { password: passwordEnabled, oauth, passkey: passkeyOffered } = methods ?? pendingMethods
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [pendingProvider, setPendingProvider] = useState('')
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  const [error, setError] = useState('')
  const busy = submitting || pendingProvider !== '' || passkeyBusy

  // passkeysSupported() is synchronous and stable for the life of the page; the
  // server's half arrives with `methods`, which is why this is not state either.
  const passkeyVisible = passkeyOffered && passkeysSupported()
  // The *promise* of an armed request, not the request itself. Arming has to
  // await capability detection and a network round trip, and during that window a
  // ref holding only the settled handle would still read null — so a submit or a
  // button click would find nothing to cancel and start a second
  // credentials.get() (which rejects while one is outstanding), while the late
  // .then() would overwrite the ref and leave the first request uncancellable.
  // Storing the promise closes that window: it is non-null the instant arming
  // starts, so cancellation covers pending setup as well as an active request,
  // and the same non-null check prevents arming twice.
  const conditional = useRef<Promise<ConditionalPasskeyLogin | null> | null>(null)
  const onSuccessRef = useRef(onSuccess)
  useEffect(() => {
    onSuccessRef.current = onSuccess
  })

  async function cancelConditional() {
    const pending = conditional.current
    conditional.current = null
    const request = await pending
    await request?.cancel()
  }

  function armConditional() {
    if (!passkeyVisible || !passwordEnabled || conditional.current) {
      return
    }
    const pending = startConditionalPasskeyLogin({
      onSuccess: () => onSuccessRef.current(),
      onError: setError,
    })
    conditional.current = pending
    // A null result means nothing was armed (no conditional mediation, or the
    // server would not issue a challenge). Release the slot so a later attempt
    // can try again rather than being blocked by a resolved-null promise.
    void pending.then((request) => {
      if (request === null && conditional.current === pending) {
        conditional.current = null
      }
    })
  }

  useEffect(() => {
    // Conditional mediation attaches to the autofill-tagged email field, so this
    // waits for the auth-methods response rather than arming against the
    // optimistic password-only default.
    if (!methods) {
      return
    }
    armConditional()
    return () => {
      // Not optional: RootLayout unmounts this component the moment the gate
      // flips, and an outstanding conditional request would otherwise stay live
      // against a dead page — and could still adopt a session after the fact.
      void cancelConditional()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- armConditional and cancelConditional read refs, not render state
  }, [methods, passkeyVisible, passwordEnabled])

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()

    try {
      setSubmitting(true)
      setError('')
      // A conditional request that resolved after this succeeded would overwrite
      // the session that was just established, possibly with a different account.
      await cancelConditional()
      await loginWithPassword(email, password)
      onSuccess()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
      armConditional()
    } finally {
      setSubmitting(false)
    }
  }

  async function onPasskeySignIn() {
    try {
      setPasskeyBusy(true)
      setError('')
      // A modal get() rejects while a conditional one is still outstanding.
      await cancelConditional()
      await loginWithPasskey()
      onSuccess()
    } catch (err) {
      if (!isAbortError(err)) {
        setError(err instanceof Error ? err.message : 'Passkey sign-in failed')
      }
      armConditional()
    } finally {
      setPasskeyBusy(false)
    }
  }

  async function onOAuthSignIn(provider: OAuthProvider) {
    try {
      setPendingProvider(provider.name)
      setError('')
      await cancelConditional()
      await loginWithOAuth2(provider.name)
      onSuccess()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign-in failed')
      armConditional()
    } finally {
      setPendingProvider('')
    }
  }

  const hasAlternates = passkeyVisible || oauth.length > 0

  return (
    <div className="flex min-h-screen flex-col bg-paper">
      <div className="flex flex-1 items-center justify-center px-6">
        <section className="w-full max-w-sm border border-line-strong bg-surface p-8 shadow-sm shadow-ink/10">
          <div className="mb-7 flex flex-col items-center gap-3 border-b border-line pb-6 text-center">
            <AppLogo appName={appName} accent={accent} />
            <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">
              {appName}
            </h1>
            <p className="text-[10px] font-semibold uppercase tracking-[0.22em] text-ink-soft">
              Personal document archive
            </p>
          </div>

          {passwordEnabled && (
            <form className="flex flex-col gap-4" onSubmit={onSubmit}>
              <label className={labelClassName}>
                <span className={labelTextClassName}>Email</span>
                <input
                  type="email"
                  // The webauthn token is what lets a passkey appear in this
                  // field's autofill suggestions. It has to sit on a field that
                  // exists when the conditional get() is made.
                  autoComplete={passkeyVisible ? 'email webauthn' : 'email'}
                  required
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  className={inputClassName}
                />
              </label>

              <label className={labelClassName}>
                <span className={labelTextClassName}>Password</span>
                <input
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  className={inputClassName}
                />
              </label>

              {error && <p className="text-sm text-madder">{error}</p>}

              <Button type="submit" disabled={busy}>
                {submitting ? 'Signing in...' : 'Sign in'}
              </Button>
            </form>
          )}

          {hasAlternates && (
            <>
              {passwordEnabled && <MethodSeparator />}
              {!passwordEnabled && error && <p className="mb-4 text-sm text-madder">{error}</p>}
              <div className="flex flex-col gap-2">
                {passkeyVisible && (
                  <Button
                    variant="secondary"
                    disabled={busy}
                    onClick={() => void onPasskeySignIn()}
                  >
                    {passkeyBusy ? 'Waiting for your passkey...' : 'Sign in with a passkey'}
                  </Button>
                )}
                {oauth.map((provider) => (
                  <Button
                    key={provider.name}
                    variant="secondary"
                    disabled={busy}
                    onClick={() => void onOAuthSignIn(provider)}
                  >
                    {pendingProvider === provider.name
                      ? `Signing in with ${provider.displayName}...`
                      : `Continue with ${provider.displayName}`}
                  </Button>
                ))}
              </div>
            </>
          )}
        </section>
      </div>
      <AppFooter />
    </div>
  )
}
