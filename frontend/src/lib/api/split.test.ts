import { describe, expect, it } from 'vitest'
import { cutsFromParts, describeParts, partsFromCuts } from './split'

describe('partsFromCuts', () => {
  it('covers every page when nothing is cut', () => {
    expect(partsFromCuts(5, [])).toEqual([{ from: 1, to: 5 }])
  })

  it('turns cut markers into a contiguous cover', () => {
    expect(partsFromCuts(12, [1, 3])).toEqual([
      { from: 1, to: 1 },
      { from: 2, to: 3 },
      { from: 4, to: 12 },
    ])
  })

  it('sorts and de-duplicates the markers', () => {
    expect(partsFromCuts(4, [3, 1, 3])).toEqual([
      { from: 1, to: 1 },
      { from: 2, to: 3 },
      { from: 4, to: 4 },
    ])
  })

  it('ignores markers outside the file', () => {
    // A cut after the last page would describe an empty trailing part.
    expect(partsFromCuts(3, [0, 3, 9])).toEqual([{ from: 1, to: 3 }])
  })

  it('splits every page apart when every gap is cut', () => {
    expect(partsFromCuts(3, [1, 2])).toEqual([
      { from: 1, to: 1 },
      { from: 2, to: 2 },
      { from: 3, to: 3 },
    ])
  })
})

describe('cutsFromParts', () => {
  it('reads the markers back out of detected parts', () => {
    expect([...cutsFromParts([{ from: 1, to: 2 }, { from: 3, to: 6 }], 6)]).toEqual([2])
  })

  it('drops the closing boundary of the last part', () => {
    expect([...cutsFromParts([{ from: 1, to: 4 }], 4)]).toEqual([])
  })

  it('round-trips with partsFromCuts', () => {
    const parts = partsFromCuts(9, [2, 5])
    expect(partsFromCuts(9, cutsFromParts(parts, 9))).toEqual(parts)
  })
})

describe('describeParts', () => {
  it('writes single pages without a range', () => {
    expect(describeParts(partsFromCuts(12, [1, 3]))).toBe('1, 2–3, 4–12')
  })
})
