import { Link, Outlet } from '@tanstack/react-router'

import { useAppMeta } from '../hooks/useAppMeta'
import { tabClassName } from '../components/ui'

/**
 * The Settings shell: a header, the tabs, and whichever tab is on the URL. Each
 * tab saves only its own fields -- the settings PATCH treats every field as
 * optional, so a tab naming its own leaves the rest of the record alone.
 */
export function SettingsPage() {
  // unknown/failed meta counts as managed; see AppMeta.aiManaged
  const { aiManaged, ingestDir } = useAppMeta()
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
            editable behind them. */}
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
        {/* Only with INGEST_DIR set: without a folder there is nothing to configure. */}
        {ingestDir && (
          <Link to="/settings/ingest" className={tabClassName}>
            Ingest
          </Link>
        )}
      </nav>

      <Outlet />
    </div>
  )
}
