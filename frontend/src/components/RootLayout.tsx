import { useEffect, useRef, useState, type ReactElement } from 'react'
import { Link, Outlet, useMatchRoute } from '@tanstack/react-router'
import { pb, pbAdminUrl } from '../lib/pb'
import { ensureAuth, getUserDisplayName, isAdmin, logout } from '../lib/auth'
import { getAppMeta, getSetupStatus, type SetupStatus } from '../lib/api/meta'
import { useAppMeta } from '../hooks/useAppMeta'
import { useInboxCount } from '../hooks/useInboxCount'
import { useActiveJobCount } from '../hooks/useActiveJobCount'
import {
  primaryNavItems,
  secondaryNavItems,
  visibleNavItems,
  NAV_BADGE_DESCRIPTIONS,
  type NavBadgeKey,
  type NavIconKey,
  type NavItem,
} from '../lib/nav'
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
  'flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm font-medium text-ink-muted transition-colors hover:bg-wash hover:text-oxblood data-[status=active]:text-oxblood'
// The narrow layout has no room for a bar, so the whole link set stacks into
// full-width tap targets.
const panelItemClass =
  'flex w-full items-center gap-2 border-t border-line/70 px-0.5 py-3 text-left text-xs font-semibold uppercase tracking-[0.14em] text-ink-soft transition-colors first:border-t-0 hover:text-oxblood focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood data-[status=active]:text-oxblood'

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

function MenuIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-5 w-5"
      aria-hidden="true"
    >
      <line x1="4" y1="7" x2="20" y2="7" />
      <line x1="4" y1="12" x2="20" y2="12" />
      <line x1="4" y1="17" x2="20" y2="17" />
    </svg>
  )
}

function CloseIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-5 w-5"
      aria-hidden="true"
    >
      <line x1="6" y1="6" x2="18" y2="18" />
      <line x1="18" y1="6" x2="6" y2="18" />
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

function PocketBaseIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 40 40"
      fill="none"
      className="h-3.5 w-3.5 shrink-0"
      aria-hidden="true"
    >
      <path
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="2.5"
        d="M26 14h10.8a1.2 1.2 0 0 1 1.2 1.2v21.6a1.2 1.2 0 0 1-1.2 1.2H15.2a1.2 1.2 0 0 1-1.2-1.2V26M26 14V3.2A1.2 1.2 0 0 0 24.8 2H3.2A1.2 1.2 0 0 0 2 3.2v21.6A1.2 1.2 0 0 0 3.2 26H14"
      />
      <path
        fill="currentColor"
        d="M10 20a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1h3.753a9 9 0 0 1 2.037.22q.967.199 1.667.697.72.48 1.131 1.296.412.798.412 1.974 0 1.137-.432 1.974a3.760 3.760 0 0 1-1.132 1.376 4.9 4.9 0 0 1-1.666.797 7.6 7.6 0 0 1-2.017.26h-.728a1 1 0 0 0-1 1V19a1 1 0 0 1-1 1zm2.025-7.740a1 1 0 0 0 1 1h.543q2.469 0 2.469-2.073 0-1.017-.638-1.435-.617-.42-1.831-.42h-.543a1 1 0 0 0-1 1zM22 33a1 1 0 0 1-1-1V21a1 1 0 0 1 1-1h3.488q1.044 0 1.926.16.902.139 1.557.518.657.378 1.025.997.39.618.39 1.555 0 .44-.144.877a2.4 2.4 0 0 1-.41.818q-.287.378-.717.678a2.9 2.9 0 0 1-.987.43.05.05 0 0 0-.038.047.05.05 0 0 0 .04.049q1.405.261 2.132.99.738.736.738 2.053 0 .996-.39 1.715a3.4 3.4 0 0 1-1.085 1.196 5.4 5.4 0 0 1-1.640.698 8.7 8.7 0 0 1-2.008.219zm2.012-8.776a1 1 0 0 0 1 1h.332q1.106 0 1.599-.419.49-.418.491-1.176 0-.719-.512-1.017-.492-.32-1.557-.32h-.353a1 1 0 0 0-1 1zm0 5.483a1 1 0 0 0 1 1h.62q2.417 0 2.417-1.755 0-.857-.594-1.236-.574-.379-1.824-.379h-.619a1 1 0 0 0-1 1z"
      />
    </svg>
  )
}

const NAV_ICONS: Record<NavIconKey, () => ReactElement> = {
  pocketbase: PocketBaseIcon,
}

type NavBadges = Record<NavBadgeKey, number | null>

function navBadge(item: NavItem, badges: NavBadges): number | null | undefined {
  return item.kind === 'route' && item.badgeKey ? badges[item.badgeKey] : undefined
}

/**
 * The digits are hidden from assistive tech and replaced with a sentence,
 * because "Inbox 3" is not a useful accessible name.
 */
function NavBadge({ count, description }: { count: number; description: string }) {
  return (
    <>
      <span
        aria-hidden="true"
        className="ml-1.5 inline-flex px-1 py-0.5 align-middle text-[10px] font-semibold tabular-nums text-amber-800 ring-1 ring-inset ring-amber-800/40"
      >
        {count > 99 ? '99+' : count}
      </span>
      <span className="sr-only">{`, ${count} ${description}`}</span>
    </>
  )
}

/**
 * One header link, in either shape. The router handles internal paths so a tap
 * does not reload the app; the PocketBase dashboard is a real page elsewhere.
 */
function NavItemLink({
  item,
  className,
  role,
  onNavigate,
  badge,
}: {
  item: NavItem
  className: string
  role?: string
  onNavigate?: () => void
  badge?: number | null
}) {
  const Icon = item.icon && NAV_ICONS[item.icon]
  // The shield stays decorative: these items only render for admins, so "admin
  // only" in the accessible name would add nothing.
  const label = (
    <>
      {item.admin && <AdminIcon />}
      {Icon && <Icon />}
      <span>{item.label}</span>
      {typeof badge === 'number' && badge > 0 && item.kind === 'route' && item.badgeKey && (
        <NavBadge count={badge} description={NAV_BADGE_DESCRIPTIONS[item.badgeKey]} />
      )}
    </>
  )

  if (item.kind === 'external') {
    return (
      <a
        href={item.href}
        target="_blank"
        rel="noopener noreferrer"
        role={role}
        className={className}
        onClick={onNavigate}
      >
        {label}
      </a>
    )
  }

  return (
    <Link
      to={item.to}
      role={role}
      className={className}
      activeOptions={item.exact ? { exact: true } : undefined}
      onClick={onNavigate}
    >
      {label}
    </Link>
  )
}

function MoreNavMenu({ items }: { items: NavItem[] }) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const matchRoute = useMatchRoute()
  const menuActive = items.some(
    (item) => item.kind === 'route' && Boolean(matchRoute({ to: item.to })),
  )

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
          {items.map((item) => (
            <NavItemLink
              key={item.label}
              item={item}
              role="menuitem"
              className={menuItemClass}
              onNavigate={() => setOpen(false)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * The header, in two layouts: below `md` the row keeps only the wordmark and a
 * toggle and everything else stacks into a panel. Both render from one item list.
 */
function AppHeader({
  appName,
  accent,
  admin,
  reviewRequired,
  userDisplayName,
}: {
  appName: string
  accent: string
  admin: boolean
  reviewRequired: boolean
  userDisplayName: string
}) {
  const [open, setOpen] = useState(false)
  const primaryItems = primaryNavItems(reviewRequired)
  const secondaryItems = visibleNavItems(secondaryNavItems(pbAdminUrl), admin)
  const panelItems = [...primaryItems, ...secondaryItems]
  const badges = { inbox: useInboxCount(), activity: useActiveJobCount() }

  useEffect(() => {
    if (!open) return

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        setOpen(false)
      }
    }

    // Widening the viewport hides the panel by CSS; closing it too keeps the
    // toggle's state honest for the trip back to a narrow one.
    const wide = window.matchMedia('(min-width: 48rem)')
    function onWide(event: MediaQueryListEvent) {
      if (event.matches) {
        setOpen(false)
      }
    }

    document.addEventListener('keydown', onKeyDown)
    wide.addEventListener('change', onWide)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      wide.removeEventListener('change', onWide)
    }
  }, [open])

  return (
    <header className="border-b-3 border-double border-line-strong bg-paper">
      <div className="mx-auto flex max-w-7xl items-center justify-between gap-3 px-4 py-3 sm:px-6 md:py-4">
        <Link
          to="/"
          className="flex min-w-0 items-center gap-2.5 font-display text-lg font-semibold tracking-tight text-ink transition-colors hover:text-oxblood focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-oxblood sm:text-xl"
        >
          <AppLogo appName={appName} accent={accent} />
          <span className="truncate">{appName}</span>
        </Link>
        <div className="hidden items-center gap-4 md:flex">
          <nav className="flex items-center gap-5" aria-label="Main">
            {primaryItems.map((item) => (
              <NavItemLink
                key={item.label}
                item={item}
                className={navLinkClass}
                badge={navBadge(item, badges)}
              />
            ))}
            <MoreNavMenu items={secondaryItems} />
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
        <button
          type="button"
          className={`${iconButtonClass} -mr-1.5 shrink-0 md:hidden`}
          aria-label={open ? 'Close menu' : 'Menu'}
          aria-expanded={open}
          aria-controls="header-nav-panel"
          onClick={() => setOpen((value) => !value)}
        >
          {open ? <CloseIcon /> : <MenuIcon />}
        </button>
      </div>
      {open && (
        <div id="header-nav-panel" className="border-t border-line bg-surface md:hidden">
          <nav className="mx-auto max-w-7xl px-4 sm:px-6" aria-label="Main">
            {panelItems.map((item) => (
              <NavItemLink
                key={item.label}
                item={item}
                className={panelItemClass}
                onNavigate={() => setOpen(false)}
                badge={navBadge(item, badges)}
              />
            ))}
          </nav>
          <div className="mx-auto flex max-w-7xl items-center justify-between gap-3 border-t border-line-strong px-4 py-3 sm:px-6">
            <span className="min-w-0 truncate text-sm text-ink-muted">{userDisplayName}</span>
            <button
              type="button"
              onClick={logout}
              className="flex shrink-0 items-center gap-2 px-0.5 py-1 text-xs font-semibold uppercase tracking-[0.14em] text-ink-soft transition-colors hover:text-oxblood focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood"
            >
              <LogoutIcon />
              Log out
            </button>
          </div>
        </div>
      )}
    </header>
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
  // Meta before the gate opens, because always_require_review decides what a
  // bare "/" means: without it the list paints every status for one frame and
  // then narrows.
  const [status] = await Promise.all([getSetupStatus(), getAppMeta()])

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
  const { appName, accent, alwaysRequireReview } = useAppMeta()
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
    // synchronous body.
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
      <AppHeader
        appName={appName}
        accent={accent}
        admin={admin}
        reviewRequired={Boolean(alwaysRequireReview)}
        userDisplayName={userDisplayName}
      />
      <main className="mx-auto w-full max-w-7xl flex-1 px-4 py-5 sm:px-6 sm:py-6">
        <Outlet />
      </main>
      <AppFooter />
    </div>
  )
}
