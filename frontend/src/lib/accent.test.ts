import { describe, expect, it } from 'vitest'
import { accentContrastText } from './accent'

describe('accentContrastText', () => {
  it('uses black text on light backgrounds', () => {
    expect(accentContrastText('#ffffff')).toBe('#000000')
    expect(accentContrastText('#fde047')).toBe('#000000')
  })

  it('uses white text on dark backgrounds', () => {
    expect(accentContrastText('#000000')).toBe('#ffffff')
    expect(accentContrastText('#111827')).toBe('#ffffff')
  })

  it('accepts values without a leading hash and with padding', () => {
    expect(accentContrastText(' 111827 ')).toBe('#ffffff')
  })

  it('falls back to white for invalid input', () => {
    expect(accentContrastText('')).toBe('#ffffff')
    expect(accentContrastText('#12')).toBe('#ffffff')
    expect(accentContrastText('not-a-color')).toBe('#ffffff')
  })
})
