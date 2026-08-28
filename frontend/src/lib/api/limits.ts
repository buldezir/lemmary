import { apiFetch } from '../apiClient'

/** One allowance and what is used against it. `limit` is absent when unlimited. */
export type LimitStatus = {
  used: number
  limit?: number
  unlimited: boolean
}

export type InstanceLimits = {
  /** False when this install bounds nothing, which is the default. */
  enforced: boolean
  /** LIMIT_* variables the server could not read; admin sessions only. */
  misconfigured?: string[]
  documents: LimitStatus
  document_pages: LimitStatus
  storage_bytes: LimitStatus
  file_bytes: LimitStatus
  file_pages: LimitStatus
  additional_users: LimitStatus
}

/** The names the backend uses for each limit, as sent in a rejection payload. */
export type LimitName =
  | 'documents'
  | 'document_pages'
  | 'storage_bytes'
  | 'file_bytes'
  | 'file_pages'
  | 'additional_users'

export function getLimits() {
  return apiFetch<InstanceLimits>('/api/app/limits', {
    fallbackError: 'Failed to load instance limits',
  })
}

const LIMIT_NAMES = new Set<string>([
  'documents',
  'document_pages',
  'storage_bytes',
  'file_bytes',
  'file_pages',
  'additional_users',
])

/**
 * The limit named by a rejected write, or null when the failure was something
 * else.
 *
 * A limit rejection is a 400 whose data reads
 * `{"limit": {"code": "limit_<name>", "params": {limit, allowed, used}}}`.
 *
 * The nesting is not a choice — PocketBase replaces any error-data value that
 * does not implement its `SafeErrorItem` interface with a generic
 * `{"code": "validation_invalid_value"}`, so a plain string would never arrive.
 * (That is exactly why `duplicateIdFromError` next door has to fall back to
 * parsing the message text.) The backend implements the interface, which is what
 * puts a real code and real numbers on the wire.
 */
export function limitFromError(err: unknown): LimitName | null {
  const limit = (
    err as { response?: { data?: { limit?: { code?: unknown; params?: { limit?: unknown } } } } } | null
  )?.response?.data?.limit
  if (!limit) return null

  const fromParams = limit.params?.limit
  if (typeof fromParams === 'string' && LIMIT_NAMES.has(fromParams)) {
    return fromParams as LimitName
  }
  // Fall back to the code, which carries the same name with a prefix.
  const code = limit.code
  if (typeof code === 'string' && code.startsWith('limit_')) {
    const name = code.slice('limit_'.length)
    if (LIMIT_NAMES.has(name)) return name as LimitName
  }
  return null
}

const UNIT = 1024
const SUFFIXES = ['KB', 'MB', 'GB', 'TB']

/**
 * Renders a byte count the way a person reads a file size.
 *
 * Binary units, matching the backend's own formatting, so a quota bar and the
 * rejection message that follows it never disagree about what "20 MB" means.
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '0 B'
  if (bytes < UNIT) return `${Math.round(bytes)} B`
  let value = bytes
  for (const suffix of SUFFIXES) {
    value /= UNIT
    if (value < UNIT) {
      return value < 10 ? `${value.toFixed(1)} ${suffix}` : `${Math.round(value)} ${suffix}`
    }
  }
  return `${Math.round(value / UNIT)} PB`
}

/** True when this limit is bounded and fully consumed. */
export function isExhausted(status: LimitStatus): boolean {
  return !status.unlimited && status.limit !== undefined && status.used >= status.limit
}

/** The bounded limits, in the order they should be displayed. */
export function boundedLimits(
  limits: InstanceLimits,
): Array<{ name: LimitName; label: string; status: LimitStatus; format: (n: number) => string }> {
  const asCount = (n: number) => n.toLocaleString()
  const rows: Array<{
    name: LimitName
    label: string
    status: LimitStatus
    format: (n: number) => string
  }> = [
    { name: 'documents', label: 'Documents', status: limits.documents, format: asCount },
    { name: 'document_pages', label: 'Pages', status: limits.document_pages, format: asCount },
    { name: 'storage_bytes', label: 'Storage', status: limits.storage_bytes, format: formatBytes },
    { name: 'additional_users', label: 'Additional users', status: limits.additional_users, format: asCount },
  ]
  return rows.filter((row) => !row.status.unlimited)
}
