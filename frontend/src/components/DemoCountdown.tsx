import { useEffect, useState } from 'react'
import { t } from '../i18n'
import { writableCountdown } from '../lib/writableCountdown'

export function DemoCountdown({ until }: { until: number }) {
  const [now, setNow] = useState(Date.now)
  const over = now >= until

  useEffect(() => {
    if (over) return
    const id = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(id)
  }, [over])

  const { text, note } = writableCountdown(until, now)
  return (
    <span
      role="timer"
      title={t('demoCountdown.title', { text, note })}
      className="shrink-0 whitespace-nowrap rounded-full border border-oxblood/40 px-2 py-0.5 text-xs font-medium tabular-nums text-oxblood"
    >
      {t('demoCountdown.badge', { text })}
      {over && ` · ${note}`}
    </span>
  )
}
