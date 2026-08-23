import { describe, expect, it } from 'vitest'
import { pageNumbers } from './pagination'

describe('pageNumbers', () => {
  it('lists every page when there are seven or fewer', () => {
    expect(pageNumbers(1, 1)).toEqual([1])
    expect(pageNumbers(3, 7)).toEqual([1, 2, 3, 4, 5, 6, 7])
  })

  it('windows around the current page for long lists', () => {
    expect(pageNumbers(5, 20)).toEqual([1, 4, 5, 6, 20])
  })

  it('clamps the window at the edges', () => {
    expect(pageNumbers(1, 20)).toEqual([1, 2, 20])
    expect(pageNumbers(20, 20)).toEqual([1, 19, 20])
  })

  it('deduplicates when the window touches the ends', () => {
    expect(pageNumbers(2, 8)).toEqual([1, 2, 3, 8])
    expect(pageNumbers(7, 8)).toEqual([1, 6, 7, 8])
  })
})
