import { useEffect, useRef, useState } from 'react'
import { Link, Outlet, useMatchRoute } from '@tanstack/react-router'
import { pb, pbAdminUrl } from '../lib/pb'
import { ensureAuth, getUserDisplayName, isAdmin, logout } from '../lib/auth'
import { getSetupStatus, type SetupStatus } from '../lib/api/meta'
import { useAppMeta } from '../hooks/useAppMeta'
import { AppFooter } from './AppFooter'
import { Button } from './ui'
import { AppLogo } from './ui'
import { LoginPage } from './LoginPage'
import { SetupBlocked, SetupWizard } from './SetupWizard'

const navLinkClass =
  'border-b border-transparent px-0.5 pb-1 pt-1.5 text-xs font-semibold uppercase tracking-[0.14em] text-ink-soft transition-colors hover:text-oxblood focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-oxblood data-[status=active]:border-oxblood data-[status=active]:text-oxblood'
const iconButtonClass =
  'p-1.5 text-ink-soft transition-colors hover:text-oxblood focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood'
const iconButtonActiveClass = 'text-oxblood'
const menuItemClass =
  'flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm font-medium text-ink-muted transition-colors hover:bg-wash hover:text-oxblood'
const menuItemActiveClass = 'text-oxblood'

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

function MoreIcon() {
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
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" />
    </svg>
  )
}

function AdminIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-3.5 w-3.5 shrink-0 text-amber-600"
      aria-hidden="true"
    >
      <path d="M12 3 4.5 6v5.5c0 4.4 3.1 8.4 7.5 9.5 4.4-1.1 7.5-5.1 7.5-9.5V6L12 3Z" />
      <path d="M9.5 12.5 11.5 14.5 15 11" />
    </svg>
  )
}

// The shield stays decorative: these items only render for admins, so spelling
// "admin only" into the accessible name would add nothing but ambiguity between
// the three of them.
function AdminMenuLabel({ children }: { children: string }) {
  return (
    <>
      <AdminIcon />
      <span>{children}</span>
    </>
  )
}

function MoreNavMenu({ admin }: { admin: boolean }) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const matchRoute = useMatchRoute()
  const settingsActive = Boolean(matchRoute({ to: '/settings' }))
  const managementActive = Boolean(matchRoute({ to: '/management' }))
  const importActive = Boolean(matchRoute({ to: '/import' }))
  const exportActive = Boolean(matchRoute({ to: '/export' }))
  const ocrTestActive = Boolean(matchRoute({ to: '/ocr-test' }))
  const accountActive = Boolean(matchRoute({ to: '/account' }))
  const menuActive =
    settingsActive ||
    managementActive ||
    importActive ||
    exportActive ||
    ocrTestActive ||
    accountActive

  useEffect(() => {
    if (!open) return

    function onPointerDown(event: MouseEvent) {
      if (!rootRef.current?.contains(event.target as Node)) {
        setOpen(false)
      }
    }

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        setOpen(false)
      }
    }

    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  return (
    <div className="relative" ref={rootRef}>
      <button
        type="button"
        className={`${iconButtonClass} ${menuActive ? iconButtonActiveClass : ''}`}
        aria-label="More"
        title="More"
        aria-expanded={open}
        aria-haspopup="menu"
        onClick={() => setOpen((value) => !value)}
      >
        <MoreIcon />
      </button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 z-20 mt-2 min-w-44 border border-line-strong bg-surface p-1 shadow-md shadow-ink/10"
        >
          <Link
            to="/account"
            role="menuitem"
            className={`${menuItemClass} ${accountActive ? menuItemActiveClass : ''}`}
            onClick={() => setOpen(false)}
          >
            Account
          </Link>
          <Link
            to="/ocr-test"
            role="menuitem"
            className={`${menuItemClass} ${ocrTestActive ? menuItemActiveClass : ''}`}
            onClick={() => setOpen(false)}
          >
            OCR test
          </Link>
          <Link
            to="/export"
            role="menuitem"
            className={`${menuItemClass} ${exportActive ? menuItemActiveClass : ''}`}
            onClick={() => setOpen(false)}
          >
            Export
          </Link>
          <Link
            to="/import"
            role="menuitem"
            className={`${menuItemClass} ${importActive ? menuItemActiveClass : ''}`}
            onClick={() => setOpen(false)}
          >
            Import
          </Link>
          {admin && (
            <Link
              to="/settings"
              role="menuitem"
              className={`${menuItemClass} ${settingsActive ? menuItemActiveClass : ''}`}
              onClick={() => setOpen(false)}
            >
              <AdminMenuLabel>Settings</AdminMenuLabel>
            </Link>
          )}
          {admin && (
            <Link
              to="/management"
              role="menuitem"
              className={`${menuItemClass} ${managementActive ? menuItemActiveClass : ''}`}
              onClick={() => setOpen(false)}
            >
              <AdminMenuLabel>Management</AdminMenuLabel>
            </Link>
          )}
          {admin && (
            <a
              href={pbAdminUrl}
              target="_blank"
              rel="noopener noreferrer"
              role="menuitem"
              className={menuItemClass}
              onClick={() => setOpen(false)}
            >
              <AdminMenuLabel>Admin</AdminMenuLabel>
            </a>
          )}
        </div>
      )}
    </div>
  )
}

type Gate =
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'setup'; status: SetupStatus }
  | { kind: 'login'; status: SetupStatus }
  | { kind: 'blocked'; status: SetupStatus }
  | { kind: 'app'; status: SetupStatus; admin: boolean }

async function resolveGate(): Promise<Gate> {
  const status = await getSetupStatus()

  if (status.needs_admin) {
    return { kind: 'setup', status }
  }

  let authenticated: boolean
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
    if (await isAdmin()) {
      return { kind: 'setup', status }
    }
    return { kind: 'blocked', status }
  }

  return { kind: 'app', status, admin: await isAdmin() }
}

export function RootLayout() {
  const [gate, setGate] = useState<Gate>({ kind: 'loading' })
  const { appName, accent } = useAppMeta()
  const userDisplayName = gate.kind === 'app' ? getUserDisplayName() : ''
  const admin = gate.kind === 'app' ? gate.admin : false

  async function refreshGate() {
    try {
      setGate(await resolveGate())
    } catch (err) {
      setGate({
        kind: 'error',
        message: err instanceof Error ? err.message : 'Failed to load setup status',
      })
    }
  }

  useEffect(() => {
    // The microtask keeps refreshGate's setState out of the effect's
    // synchronous body; the gate resolves over the network anyway.
    let cancelled = false
    void Promise.resolve().then(() => {
      if (!cancelled) {
        void refreshGate()
      }
    })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (gate.kind === 'loading' || gate.kind === 'error') {
      return
    }

    return pb.authStore.onChange(() => {
      void refreshGate()
    })
  }, [gate.kind])

  if (gate.kind === 'loading') {
    return (
      <div className="flex min-h-screen items-center justify-center bg-paper text-sm text-ink-soft">
        Loading...
      </div>
    )
  }

  if (gate.kind === 'error') {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-3 bg-paper px-6 text-center">
        <p className="text-sm text-madder">{gate.message}</p>
        <Button
          onClick={() => {
            setGate({ kind: 'loading' })
            void refreshGate()
          }}
        >
          Retry
        </Button>
      </div>
    )
  }

  if (gate.kind === 'setup') {
    return (
      <SetupWizard
        appName={appName}
        accent={accent}
        initialStatus={gate.status}
        onComplete={() => void refreshGate()}
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
          void refreshGate()
        }}
      />
    )
  }

  if (gate.kind === 'login') {
    return (
      <LoginPage appName={appName} accent={accent} onSuccess={() => void refreshGate()} />
    )
  }

  return (
    <div className="flex min-h-screen flex-col bg-paper text-ink">
      <header className="border-b-3 border-double border-line-strong bg-paper">
        <div className="mx-auto flex max-w-7xl items-center justify-between px-6 py-5">
          <Link
            to="/"
            className="flex items-center gap-2.5 font-display text-xl font-semibold tracking-tight text-ink transition-colors hover:text-oxblood focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-oxblood"
          >
            <AppLogo appName={appName} accent={accent} />
            {appName}
          </Link>
          <div className="flex items-center gap-4">
            <nav className="flex items-center gap-5">
              <Link
                to="/"
                className={navLinkClass}
                activeOptions={{ exact: true }}
              >
                Documents
              </Link>
              <Link
                to="/upload"
                className={navLinkClass}
              >
                Upload
              </Link>
              <Link
                to="/search"
                className={navLinkClass}
              >
                Deep Search
              </Link>
              <MoreNavMenu admin={admin} />
            </nav>
            <div className="flex items-center gap-2 border-l border-line pl-4">
              {userDisplayName && (
                <span className="max-w-40 truncate text-sm text-ink-muted" title={userDisplayName}>
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
