import { Link, Outlet } from '@tanstack/react-router'

import { useAppMeta } from '../hooks/useAppMeta'
import { tabClassName } from '../components/ui'
import { t } from '../i18n'

/**
 * The Admin shell: a header, the tabs, and whichever tab is on the URL. Each
 * settings tab saves only its own fields -- the settings PATCH treats every
 * field as optional, so a tab naming its own leaves the rest of the record alone.
 */
export function AdminPage() {
  // unknown/failed meta counts as managed; see AppMeta.managed
  const { managed, ingestDir, ingestImap } = useAppMeta()
  const hostEditable = managed === false

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-5">
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">{t('admin.title')}</h1>
        <p className="mt-1 text-sm text-ink-soft">
          {t('admin.intro')}
        </p>
        {!hostEditable && (
          <p className="mt-2 text-sm text-ink-soft">
            {t('settings.aiManaged')}
          </p>
        )}
      </div>

      <nav
        aria-label={t('admin.sectionsLabel')}
        className="mb-5 flex flex-wrap items-center gap-5 border-b border-line"
      >
        <Link to="/settings" activeOptions={{ exact: true }} className={tabClassName}>
          {t('settings.tabAppearance')}
        </Link>
        {/* Hidden rather than disabled on a managed instance: there is nothing
            editable behind them. */}
        {hostEditable && (
          <Link to="/settings/ai" className={tabClassName}>
            {t('settings.tabAi')}
          </Link>
        )}
        <Link to="/settings/processing" className={tabClassName}>
          {t('settings.tabProcessing')}
        </Link>
        <Link to="/settings/fields" className={tabClassName}>
          {t('settings.tabFields')}
        </Link>
        {hostEditable && (
          <Link to="/settings/worker" className={tabClassName}>
            {t('settings.tabWorker')}
          </Link>
        )}
        {hostEditable && (
          <Link to="/settings/duplicates" className={tabClassName}>
            {t('settings.tabDuplicates')}
          </Link>
        )}
        {(ingestDir || ingestImap) && (
          <Link to="/settings/ingest" className={tabClassName}>
            {t('settings.tabIngest')}
          </Link>
        )}
        <Link to="/settings/mcp" className={tabClassName}>
          {t('settings.tabMcp')}
        </Link>
        <Link to="/management" className={tabClassName}>
          {t('admin.tabUsers')}
        </Link>
        <Link to="/maintenance" className={tabClassName}>
          {t('admin.tabMaintenance')}
        </Link>
      </nav>

      <Outlet />
    </div>
  )
}
