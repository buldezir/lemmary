import { describe, expect, it } from 'vitest'

import { contextOverflowWarning, estimateTokens, formatContextUsage } from './contextUsage'

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

describe('contextOverflowWarning', () => {
  const long = 'x'.repeat(40000)

  it('says nothing without a known window', () => {
    expect(contextOverflowWarning(long, undefined)).toBe('')
    expect(contextOverflowWarning(long, { peak_prompt: 1000 })).toBe('')
    expect(contextOverflowWarning(long, { peak_prompt: 1000, context_window: 0 })).toBe('')
  })

  it('says nothing while the draft still fits beside the conversation', () => {
    expect(contextOverflowWarning(long, { peak_prompt: 1000, context_window: 200000 })).toBe('')
  })

  it('warns when the draft and the conversation together overflow', () => {
    const warning = contextOverflowWarning(long, { peak_prompt: 7000, context_window: 8000 })
    expect(warning).toContain('10,000')
    expect(warning).toContain('17,000')
    expect(warning).toContain('7,440')
  })

  // 7% of the window is the answer's; a draft that fills the rest exactly is
  // still fine, one token past it is not.
  it('keeps room for the answer', () => {
    const window = { peak_prompt: 0, context_window: 10000 }
    expect(contextOverflowWarning('x'.repeat(9300 * 4), window)).toBe('')
    expect(contextOverflowWarning('x'.repeat(9301 * 4), window)).toContain('9,300')
  })

  it('counts a trimmed draft', () => {
    expect(estimateTokens('    ')).toBe(0)
    expect(estimateTokens('  abcdefgh  ')).toBe(2)
  })
})
