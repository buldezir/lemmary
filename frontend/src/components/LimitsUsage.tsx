import { boundedLimits, isExhausted, type InstanceLimits, type LimitStatus } from '../lib/api/limits'

function ratio(status: LimitStatus): number {
  if (status.unlimited || status.limit === undefined || status.limit <= 0) return 0
  return Math.min(1, status.used / status.limit)
}

function barToneClassName(status: LimitStatus): string {
  const filled = ratio(status)
  if (isExhausted(status)) return 'bg-madder'
  if (filled >= 0.9) return 'bg-madder/60'
  return 'bg-oxblood/60'
}

/**
 * One allowance as a labelled meter.
 *
 * `used` can exceed `limit` — a plan can be lowered under a library that is
 * already larger than it — so the bar clamps while the numbers tell the truth.
 * Nothing is ever deleted to make usage fit a limit.
 */
function LimitRow({
  label,
  status,
  format,
}: {
  label: string
  status: LimitStatus
  format: (n: number) => string
}) {
  const limit = status.limit ?? 0
  const over = status.used > limit
  return (
    <li className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-3 text-xs">
        <span className="font-semibold uppercase tracking-[0.12em] text-ink-soft">{label}</span>
        <span className={over ? 'text-madder' : 'text-ink-soft'}>
          {format(status.used)} of {format(limit)}
        </span>
      </div>
      <div
        className="h-1 w-full overflow-hidden bg-line"
        role="meter"
        aria-label={label}
        aria-valuenow={status.used}
        aria-valuemin={0}
        aria-valuemax={limit}
      >
        <div
          className={`h-full ${barToneClassName(status)}`}
          style={{ width: `${ratio(status) * 100}%` }}
        />
      </div>
    </li>
  )
}

/**
 * Instance allowances and what is used against them.
 *
 * Renders nothing at all when this install bounds nothing, which is the default
 * — a self-hosted instance that sets no LIMIT_* variables should not grow a
 * quota widget it has no use for. Unlimited individual limits are dropped for
 * the same reason, rather than shown as an infinity sign.
 */
export function LimitsUsage({
  limits,
  className = '',
}: {
  limits: InstanceLimits | null
  className?: string
}) {
  if (!limits || !limits.enforced) return null
  const rows = boundedLimits(limits)
  if (rows.length === 0) return null

  return (
    <ul className={`flex flex-col gap-3 border border-line bg-surface p-4 ${className}`}>
      {rows.map((row) => (
        <LimitRow key={row.name} label={row.label} status={row.status} format={row.format} />
      ))}
    </ul>
  )
}
