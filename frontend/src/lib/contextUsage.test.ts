import { describe, expect, it } from 'vitest'

import { formatContextUsage } from './contextUsage'

describe('formatContextUsage', () => {
  it('shows the share of the window when one is known', () => {
    expect(formatContextUsage({ peak_prompt: 38200, context_window: 200000 })).toBe(
      '38,200 / 200,000 tokens (19%)',
    )
  })

  it('drops the denominator when the window is unknown', () => {
    expect(formatContextUsage({ peak_prompt: 38200 })).toBe('38,200 tokens')
    expect(formatContextUsage({ peak_prompt: 38200, context_window: 0 })).toBe('38,200 tokens')
  })

  it('marks an estimate', () => {
    expect(formatContextUsage({ peak_prompt: 1000, context_window: 8000, estimated: true })).toBe(
      '~1,000 / 8,000 tokens (13%)',
    )
  })

  it('renders nothing when there is nothing to report', () => {
    expect(formatContextUsage(undefined)).toBe('')
    expect(formatContextUsage({ peak_prompt: 0, context_window: 200000 })).toBe('')
  })
})
