import { type SubmitEvent, useState } from 'react'
import {
  getLoginMethods,
  loginWithOAuth2,
  loginWithPassword,
  type LoginMethods,
  type OAuthProvider,
} from '../lib/auth'
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
const pendingMethods: LoginMethods = { password: true, oauth: [] }

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
  const { password: passwordEnabled, oauth } = methods ?? pendingMethods
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [pendingProvider, setPendingProvider] = useState('')
  const [error, setError] = useState('')
  const busy = submitting || pendingProvider !== ''

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()

    try {
      setSubmitting(true)
      setError('')
      await loginWithPassword(email, password)
      onSuccess()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setSubmitting(false)
    }
  }

  async function onOAuthSignIn(provider: OAuthProvider) {
    try {
      setPendingProvider(provider.name)
      setError('')
      await loginWithOAuth2(provider.name)
      onSuccess()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign-in failed')
    } finally {
      setPendingProvider('')
    }
  }

  return (
    <div className="flex min-h-screen flex-col bg-paper">
      <div className="flex flex-1 items-center justify-center px-6">
        <section className="w-full max-w-sm border border-line-strong bg-surface p-8 shadow-sm shadow-ink/5">
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
                  autoComplete="email"
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

          {oauth.length > 0 && (
            <>
              {passwordEnabled && <MethodSeparator />}
              {!passwordEnabled && error && <p className="mb-4 text-sm text-madder">{error}</p>}
              <div className="flex flex-col gap-2">
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
