import { t } from '../i18n'
import { getUpdateStatus } from '../lib/api/meta'
import { useAsync } from '../hooks/useAsync'

function UpdateIcon() {
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
      <circle cx="12" cy="12" r="9" />
      <polyline points="8 12 12 8 16 12" />
      <line x1="12" y1="8" x2="12" y2="16" />
    </svg>
  )
}

export function UpdateNotice() {
  const { data } = useAsync(getUpdateStatus, [])
  if (!data?.available) return null

  if (data.url && data.version) {
    const label = t('rootLayout.releaseAvailable', { version: data.version })
    return (
      <a
        href={data.url}
        target="_blank"
        rel="noopener noreferrer"
        title={label}
        aria-label={label}
        className="flex shrink-0 items-center gap-1 text-xs font-medium text-oxblood transition-colors hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood"
      >
        <UpdateIcon />
        {data.version}
      </a>
    )
  }

  return (
    <span
      role="img"
      title={t('rootLayout.updateAvailable')}
      aria-label={t('rootLayout.updateAvailable')}
      className="shrink-0 text-oxblood"
    >
      <UpdateIcon />
    </span>
  )
}
