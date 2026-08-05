import { useEffect, useState } from 'react'
import { Link, Outlet } from '@tanstack/react-router'
import {
  accentContrastText,
  ensureAuth,
  getSetupStatus,
  getUserDisplayName,
  isSuperuser,
  logout,
  pb,
  pbAdminUrl,
  type SetupStatus,
} from '../lib/pocketbase'
import { useAppMeta } from '../hooks/useAppMeta'
import { AppFooter } from './AppFooter'
import { LoginPage } from './LoginPage'
import { SetupBlocked, SetupWizard } from './SetupWizard'

const navLinkClass =
  'rounded-md px-3 py-1.5 text-sm font-medium text-stone-600 transition-colors hover:bg-stone-200/70 hover:text-stone-950'
const navLinkActiveClass = 'bg-gray-900 text-white hover:bg-gray-900 hover:text-white'
const iconButtonClass =
  'rounded-md p-1.5 text-stone-500 transition-colors hover:bg-stone-200/70 hover:text-stone-950 cursor-pointer'

function LogoutIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-4 w-4"
      aria-hidden="true"
    >
      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
      <polyline points="16 17 21 12 16 7" />
      <line x1="21" y1="12" x2="9" y2="12" />
    </svg>
  )
}

type Gate =
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'setup'; status: SetupStatus }
  | { kind: 'login'; status: SetupStatus }
  | { kind: 'blocked'; status: SetupStatus }
  | { kind: 'app'; status: SetupStatus; superuser: boolean }

export function RootLayout() {
  const [gate, setGate] = useState<Gate>({ kind: 'loading' })
  const { appName, accent } = useAppMeta()
  const userDisplayName = gate.kind === 'app' ? getUserDisplayName() : ''
  const appInitial = appName.trim().charAt(0).toUpperCase() || 'P'
  const logoStyle = { backgroundColor: accent, color: accentContrastText(accent) }
  const superuser = gate.kind === 'app' ? gate.superuser : false

  async function resolveGate(): Promise<Gate> {
    const status = await getSetupStatus()

    if (status.needs_admin) {
      return { kind: 'setup', status }
    }

    let authenticated = false
    try {
      await ensureAuth()
      authenticated = pb.authStore.isValid
    } catch {
      authenticated = false
    }

    if (!authenticated) {
      return { kind: 'login', status }
    }

    if (status.needs_config) {
      if (isSuperuser()) {
        return { kind: 'setup', status }
      }
      return { kind: 'blocked', status }
    }

    return { kind: 'app', status, superuser: isSuperuser() }
  }

  useEffect(() => {
    let active = true

    async function init() {
      try {
        const next = await resolveGate()
        if (active) setGate(next)
      } catch (err) {
        if (active) {
          setGate({
            kind: 'error',
            message: err instanceof Error ? err.message : 'Failed to load setup status',
          })
        }
      }
    }

    void init()
    return () => {
      active = false
    }
  }, [])

  useEffect(() => {
    if (gate.kind === 'loading' || gate.kind === 'error') {
      return
    }

    return pb.authStore.onChange(() => {
      void (async () => {
        try {
          setGate(await resolveGate())
        } catch (err) {
          setGate({
            kind: 'error',
            message: err instanceof Error ? err.message : 'Failed to load setup status',
          })
        }
      })()
    })
  }, [gate.kind])

  if (gate.kind === 'loading') {
    return (
      <div className="flex min-h-screen items-center justify-center bg-stone-100 text-sm text-stone-500">
        Loading...
      </div>
    )
  }

  if (gate.kind === 'error') {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-3 bg-stone-100 px-6 text-center">
        <p className="text-sm text-red-600">{gate.message}</p>
        <button
          type="button"
          className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white"
          onClick={() => {
            setGate({ kind: 'loading' })
            void resolveGate()
              .then(setGate)
              .catch((err) =>
                setGate({
                  kind: 'error',
                  message: err instanceof Error ? err.message : 'Failed to load setup status',
                }),
              )
          }}
        >
          Retry
        </button>
      </div>
    )
  }

  if (gate.kind === 'setup') {
    return (
      <SetupWizard
        appName={appName}
        accent={accent}
        initialStatus={gate.status}
        onComplete={() => {
          void resolveGate().then(setGate)
        }}
      />
    )
  }

  if (gate.kind === 'blocked') {
    return (
      <SetupBlocked
        appName={appName}
        accent={accent}
        onLogout={() => {
          logout()
          void resolveGate().then(setGate)
        }}
      />
    )
  }

  if (gate.kind === 'login') {
    return (
      <LoginPage
        appName={appName}
        accent={accent}
        onSuccess={() => {
          void resolveGate().then(setGate)
        }}
      />
    )
  }

  return (
    <div className="flex min-h-screen flex-col bg-stone-100 text-stone-900">
      <header className="border-b border-stone-200 bg-stone-50/95">
        <div className="mx-auto flex max-w-7xl items-center justify-between px-6 py-4">
          <Link to="/" className="flex items-center gap-2 font-semibold text-stone-950">
            <span
              className="flex h-7 w-7 items-center justify-center rounded-md text-sm"
              style={logoStyle}
            >
              {appInitial}
            </span>
            {appName}
          </Link>
          <div className="flex items-center gap-4">
            <nav className="flex items-center gap-1">
              <Link
                to="/"
                className={navLinkClass}
                activeOptions={{ exact: true }}
                activeProps={{ className: `${navLinkClass} ${navLinkActiveClass}` }}
              >
                Documents
              </Link>
              <Link
                to="/upload"
                className={navLinkClass}
                activeProps={{ className: `${navLinkClass} ${navLinkActiveClass}` }}
              >
                Upload
              </Link>
              <Link
                to="/search"
                className={navLinkClass}
                activeProps={{ className: `${navLinkClass} ${navLinkActiveClass}` }}
              >
                Deep Search
              </Link>
              <Link
                to="/mail"
                className={navLinkClass}
                activeProps={{ className: `${navLinkClass} ${navLinkActiveClass}` }}
              >
                Mail
              </Link>
              <Link
                to="/ocr-test"
                className={navLinkClass}
                activeProps={{ className: `${navLinkClass} ${navLinkActiveClass}` }}
              >
                OCR test
              </Link>
              {superuser && (
                <Link
                  to="/settings"
                  className={navLinkClass}
                  activeProps={{ className: `${navLinkClass} ${navLinkActiveClass}` }}
                >
                  Settings
                </Link>
              )}
              <a
                href={pbAdminUrl}
                target="_blank"
                rel="noopener noreferrer"
                className={navLinkClass}
              >
                Admin
              </a>
            </nav>
            <div className="flex items-center gap-2 border-l border-stone-200 pl-4">
              {userDisplayName && (
                <span className="max-w-40 truncate text-sm text-stone-600" title={userDisplayName}>
                  {userDisplayName}
                </span>
              )}
              <button
                type="button"
                onClick={logout}
                className={iconButtonClass}
                aria-label="Log out"
                title="Log out"
              >
                <LogoutIcon />
              </button>
            </div>
          </div>
        </div>
      </header>
      <main className="mx-auto w-full max-w-7xl flex-1 px-6 py-8">
        <Outlet />
      </main>
      <AppFooter />
    </div>
  )
}
