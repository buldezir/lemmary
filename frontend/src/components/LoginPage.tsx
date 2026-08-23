import { type SubmitEvent, useState } from 'react'
import { loginWithPassword } from '../lib/auth'
import { AppFooter } from './AppFooter'
import { AppLogo, Button, inputClassName, labelClassName, labelTextClassName } from './ui'

type LoginPageProps = {
  appName: string
  accent: string
  onSuccess: () => void
}

export function LoginPage({ appName, accent, onSuccess }: LoginPageProps) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

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

  return (
    <div className="flex min-h-screen flex-col bg-stone-100">
      <div className="flex flex-1 items-center justify-center px-6">
        <section className="w-full max-w-sm rounded-lg border border-stone-200 bg-stone-50 p-6 shadow-sm">
          <div className="mb-6 flex items-center gap-2">
            <AppLogo appName={appName} accent={accent} />
            <h1 className="text-lg font-semibold text-stone-950">{appName}</h1>
          </div>

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

            {error && <p className="text-sm text-red-600">{error}</p>}

            <Button type="submit" disabled={submitting}>
              {submitting ? 'Signing in...' : 'Sign in'}
            </Button>
          </form>
        </section>
      </div>
      <AppFooter />
    </div>
  )
}
