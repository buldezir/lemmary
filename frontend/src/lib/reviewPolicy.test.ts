import { afterEach, describe, expect, test } from 'vitest'
import { documentsLanding, requiresReview, setAlwaysRequireReview } from './reviewPolicy'

afterEach(() => setAlwaysRequireReview(false))

describe('review policy', () => {
  // A failed or in-flight meta request must not narrow anybody's list.
  test('is off until something says otherwise', () => {
    expect(requiresReview()).toBe(false)
    expect(documentsLanding()).toBe('/')
  })

  test('moves the upload landing to the Inbox', () => {
    setAlwaysRequireReview(true)
    expect(requiresReview()).toBe(true)
    expect(documentsLanding()).toBe('/inbox')
  })

  test('goes back off when an admin turns it off', () => {
    setAlwaysRequireReview(true)
    setAlwaysRequireReview(false)
    expect(documentsLanding()).toBe('/')
  })
})
