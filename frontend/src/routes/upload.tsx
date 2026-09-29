import { useEffect, useState } from 'react'
import { Link, Outlet } from '@tanstack/react-router'

import { LimitsUsage } from '../components/LimitsUsage'
import { getLimits, type InstanceLimits } from '../lib/api/limits'
import { tabClassName } from '../components/ui'
import { t } from '../i18n'

export function UploadPage() {
  // Loaded on the shell rather than per tab, so every source shows the same
  // figures without a fetch each. A failure is swallowed: the server refuses an
  // over-limit upload whatever this shows.
  const [limits, setLimits] = useState<InstanceLimits | null>(null)

  useEffect(() => {
    let active = true
    getLimits()
      .then((next) => {
        if (active) setLimits(next)
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [])

  return (
    <div className="mx-auto flex max-w-xl flex-col gap-5">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">{t('upload.title')}</h1>
        <p className="mt-1 text-sm text-ink-soft">
          {t('upload.intro')}
        </p>
      </div>

      <LimitsUsage limits={limits} />

      <nav aria-label={t('upload.sourcesLabel')} className="flex flex-wrap items-center gap-5 border-b border-line">
        <Link
          to="/upload"
          activeOptions={{ exact: true }}
          className={tabClassName}
        >
          {t('upload.tabFiles')}
        </Link>
        <Link
          to="/upload/scan"
          className={tabClassName}
        >
          {t('upload.tabScan')}
        </Link>
        <Link
          to="/upload/amazon"
          className={tabClassName}
        >
          {t('upload.tabAmazon')}
        </Link>
        <Link
          to="/upload/zip"
          className={tabClassName}
        >
          {t('upload.tabZip')}
        </Link>
        <Link
          to="/upload/split"
          className={tabClassName}
        >
          {t('upload.tabSplit')}
        </Link>
      </nav>

      <Outlet />
    </div>
  )
}
