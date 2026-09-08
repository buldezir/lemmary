import { afterEach, describe, expect, test } from 'vitest'
import {
  defaultStatusFilter,
  documentsLanding,
  requiresReview,
  setAlwaysRequireReview,
} from './reviewPolicy'

afterEach(() => setAlwaysRequireReview(false))

describe('review policy', () => {
  // A failed or in-flight meta request must not narrow anybody's list.
  test('is off until something says otherwise', () => {
    expect(requiresReview()).toBe(false)
    expect(defaultStatusFilter()).toBe('all')
    expect(documentsLanding()).toBe('/')
  })

  test('moves the list default and the upload landing together', () => {
    setAlwaysRequireReview(true)
    expect(requiresReview()).toBe(true)
    expect(defaultStatusFilter()).toBe('completed')
    // The pair is the point: with `/` filtered to Completed, an upload sent
    // there lands on the one list that cannot show it.
    expect(documentsLanding()).toBe('/inbox')
  })

  test('goes back off when an admin turns it off', () => {
    setAlwaysRequireReview(true)
    setAlwaysRequireReview(false)
    expect(defaultStatusFilter()).toBe('all')
    expect(documentsLanding()).toBe('/')
  })
})
