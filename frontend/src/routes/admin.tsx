import { Link, Outlet } from '@tanstack/react-router'

import { t } from '../i18n'

const sectionLinkClassName =
  'border-l-2 border-transparent py-1 pl-2 pr-3 text-sm font-medium text-ink-muted transition-colors hover:border-line hover:bg-surface focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood data-[status=active]:border-oxblood data-[status=active]:bg-wash data-[status=active]:text-oxblood'

/** The Admin shell: a header, the section rail, and whichever section is on the URL. */
export function AdminPage() {
  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">{t('admin.title')}</h1>
        <p className="mt-1 text-sm text-ink-soft">{t('admin.intro')}</p>
      </div>

      <div className="flex flex-col gap-5 lg:flex-row lg:items-start">
        <nav
          aria-label={t('admin.sectionsLabel')}
          className="flex flex-wrap gap-1 lg:sticky lg:top-6 lg:w-44 lg:shrink-0 lg:flex-col"
        >
          <Link to="/settings" className={sectionLinkClassName}>
            {t('admin.settings')}
          </Link>
          <Link to="/management" className={sectionLinkClassName}>
            {t('admin.management')}
          </Link>
          <Link to="/maintenance" className={sectionLinkClassName}>
            {t('admin.maintenance')}
          </Link>
        </nav>

        <div className="min-w-0 flex-1">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
