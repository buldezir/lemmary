import { Link, Outlet } from '@tanstack/react-router'

import { tabClassName } from '../components/ui'
import { t } from '../i18n'

export function ImportPage() {
  return (
    <div className="mx-auto flex max-w-xl flex-col gap-5">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">{t('import.title')}</h1>
        <p className="mt-1 text-sm text-ink-soft">
          {t('import.intro')}
        </p>
      </div>

      <nav aria-label={t('import.sourcesLabel')} className="flex flex-wrap items-center gap-5 border-b border-line">
        <Link to="/import" activeOptions={{ exact: true }} className={tabClassName}>
          {t('import.tabArchive')}
        </Link>
        <Link to="/import/ngx" className={tabClassName}>
          Paperless-ngx
        </Link>
      </nav>

      <Outlet />
    </div>
  )
}
