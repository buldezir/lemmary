import { describe, expect, it } from 'vitest'
import { extent, formatNumber, niceTicks, parseChart } from './chart'

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
    expect([0, 0.6000000000000001, 950, 1234, 12900, -3400000].map(formatNumber)).toEqual([
      '0',
      '0.6',
      '950',
      '1.23K',
      '12.9K',
      '-3.4M',
    ])
  })
})
