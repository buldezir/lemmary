import { describe, expect, it } from 'vitest'
import { barWidth, extent, formatNumber, niceTicks, parseChart } from './chart'

describe('parseChart', () => {
  it('reads the block the server writes', () => {
    const spec = parseChart('{"type":"bar","title":"Spend","labels":["2024","2025"],"series":[{"values":[120,80]}]}')
    expect(spec?.series[0].values).toEqual([120, 80])
  })

  it('refuses what it cannot draw', () => {
    for (const source of [
      'not json',
      'null',
      '{"type":"radar","title":"t","labels":["a"],"series":[{"values":[1]}]}',
      '{"type":"bar","title":"t","labels":["a","b"],"series":[{"values":[1]}]}',
      '{"type":"line","title":"t","labels":["a"],"series":[]}',
      '{"type":"line","title":"t","labels":["a"],"series":[{"values":["1"]}]}',
      '{"type":"line","title":"t","labels":["a"],"series":[{"values":[1e16]}]}',
      '{"type":"pie","title":"t","labels":["a","b"],"series":[{"values":[0,0]}]}',
    ]) {
      expect(parseChart(source)).toBeNull()
    }
  })
})

describe('axis', () => {
  it('starts bars at zero and steps round', () => {
    const spec = parseChart('{"type":"bar","title":"t","labels":["a","b"],"series":[{"values":[40,87]}]}')!
    expect(extent(spec)).toEqual([0, 87])
    expect(niceTicks(0, 87)).toEqual([0, 20, 40, 60, 80, 100])
  })

  it('formats values compactly', () => {
    expect([0, 0.6000000000000001, 950, 1234, 12900, -3400000, 1e-99].map(formatNumber)).toEqual([
      '0',
      '0.6',
      '950',
      '1.23K',
      '12.9K',
      '-3.4M',
      '0',
    ])
  })

  // A flat series far from zero once never left the loop.
  it('ends on ranges float steps cannot walk', () => {
    for (const [lo, hi] of [
      [1e16, 1e16],
      [1e15, 1e15],
      [-1e15, 1e15],
      [1e15, 1e15 + 0.125],
      [0, 5e-324],
      [7, 7],
    ]) {
      const ticks = niceTicks(lo, hi)
      expect(ticks.length).toBeGreaterThanOrEqual(2)
      expect(ticks[ticks.length - 1]).toBeGreaterThan(ticks[0])
    }
  })

  it('keeps a bar to draw at 100 labels and 8 series', () => {
    const [w, gap] = barWidth(588 / 100, 8)
    expect(w - gap).toBeGreaterThan(0)
  })
})
