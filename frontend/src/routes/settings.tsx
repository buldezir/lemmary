import { Link, Outlet } from '@tanstack/react-router'

import { useAppMeta } from '../hooks/useAppMeta'
import { tabClassName } from '../components/ui'

/**
 * The Settings shell: a header, the tabs, and whichever tab is on the URL.
 *
 * The sections used to be stacked on one page sharing one form, which had grown
 * past what anyone could scan. Each is now its own route, and each saves only
 * its own fields -- the settings PATCH treats every field as optional, so a tab
 * naming its own leaves the rest of the record alone.
 *
 * Admin access is enforced by the route's beforeLoad guard, which the tab routes
 * inherit.
 */
export function SettingsPage() {
  // unknown/failed meta counts as managed; see AppMeta.aiManaged
  const { aiManaged } = useAppMeta()
  const aiEditable = aiManaged === false

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-5">
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">Settings</h1>
        <p className="mt-1 text-sm text-ink-soft">
          Runtime configuration for OCR, AI, and the worker. Changes apply immediately.
        </p>
        {!aiEditable && (
          <p className="mt-2 text-sm text-ink-soft">
            AI providers and models are set by your hosting provider and are not editable here.
          </p>
        )}
      </div>

      <nav
        aria-label="Settings sections"
        className="mb-5 flex flex-wrap items-center gap-5 border-b border-line"
      >
        <Link to="/settings" activeOptions={{ exact: true }} className={tabClassName}>
          Appearance
        </Link>
        {/* Hidden rather than disabled on a managed instance: there is nothing
            editable behind them. Typed in by hand they explain themselves. */}
        {aiEditable && (
          <Link to="/settings/ai" className={tabClassName}>
            AI
          </Link>
        )}
        <Link to="/settings/processing" className={tabClassName}>
          Processing
        </Link>
        <Link to="/settings/worker" className={tabClassName}>
          Worker
        </Link>
        {aiEditable && (
          <Link to="/settings/duplicates" className={tabClassName}>
            Duplicates
          </Link>
        )}
      </nav>

      <Outlet />
    </div>
  )
}
