import { Link, Outlet } from '@tanstack/react-router'

import { tabClassName } from '../components/ui'
import { t } from '../i18n'

/** The Management shell: a header, the tabs, and whichever tab is on the URL. */
export function ManagementPage() {
  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-5">
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">{t('management.title')}</h1>
        <p className="mt-1 text-sm text-ink-soft">{t('management.intro')}</p>
      </div>

      <nav
        aria-label={t('management.sectionsLabel')}
        className="mb-5 flex flex-wrap items-center gap-5 border-b border-line"
      >
        <Link to="/management" activeOptions={{ exact: true }} className={tabClassName}>
          {t('management.tabUsers')}
        </Link>
      </nav>

      <Outlet />
    </div>
  )
}
