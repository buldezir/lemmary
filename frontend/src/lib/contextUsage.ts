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

/**
 * The same ratio the backend falls back to (ai.charsPerToken): wrong for CJK
 * and for code, close enough to answer whether a question is near the ceiling.
 */
const charsPerToken = 4

export function estimateTokens(text: string) {
  return Math.ceil(text.trim().length / charsPerToken)
}

/**
 * Percent of the window kept back for the answer. A prompt that fills the
 * window exactly leaves the model nothing to reply with, which fails the same
 * way as one that overflows it. Whole percent rather than a fraction, so the
 * budget is exact: 8000 * 0.93 floors to 7439.
 */
const answerReservePercent = 7

/**
 * Warns before sending when the draft plus what the conversation already takes
 * would not leave the model room to answer. `usage` is the last turn's: its
 * peak is what the next request resends, so the two add up. Silent without a
 * window -- there is nothing to compare against, and a guessed limit would be
 * worse than none.
 */
export function contextOverflowWarning(draft: string, usage: ContextUsage | undefined): string {
  const window = usage?.context_window ?? 0
  if (window <= 0) return ''
  const budget = Math.floor((window * (100 - answerReservePercent)) / 100)
  const asked = estimateTokens(draft)
  const total = Math.max(usage?.peak_prompt ?? 0, 0) + asked
  if (total <= budget) return ''
  return `This question is about ${tokens.format(asked)} tokens; with the chat so far that is roughly ${tokens.format(total)}, past the ${tokens.format(budget)} this model can take while leaving room for an answer.`
}
