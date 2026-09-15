/**
 * How much of the model's context a research turn took. Peak, not total: the
 * agent loop resends a growing conversation each round, so what says how close
 * the turn came to the limit is its widest single request.
 */
export interface ContextUsage {
  peak_prompt: number
  /** Absent when no catalogue knows this model's window. */
  context_window?: number
  /** The provider reported no usage, so this was estimated from the text sent. */
  estimated?: boolean
}

const tokens = new Intl.NumberFormat()

/**
 * Renders the usage as one line. Without a window there is no percentage to
 * give and the absolute count stands alone, which is the honest rendering of
 * "we do not know this model's limit" -- better than inventing a denominator.
 */
export function formatContextUsage(usage: ContextUsage | undefined): string {
  if (!usage || usage.peak_prompt <= 0) return ''

  const prefix = usage.estimated ? '~' : ''
  const used = `${prefix}${tokens.format(usage.peak_prompt)}`
  const window = usage.context_window ?? 0
  if (window <= 0) return `${used} tokens`

  const percent = Math.round((usage.peak_prompt / window) * 100)
  return `${used} / ${tokens.format(window)} tokens (${percent}%)`
}
