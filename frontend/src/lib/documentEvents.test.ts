import { describe, expect, test, vi } from 'vitest'
import { notifyDocumentsChanged, onDocumentsChanged } from './documentEvents'

describe('document change events', () => {
  test('reaches every listener and stops after unsubscribing', () => {
    const first = vi.fn()
    const second = vi.fn()
    const offFirst = onDocumentsChanged(first)
    const offSecond = onDocumentsChanged(second)

    notifyDocumentsChanged()
    expect(first).toHaveBeenCalledTimes(1)
    expect(second).toHaveBeenCalledTimes(1)

    offFirst()
    notifyDocumentsChanged()
    expect(first).toHaveBeenCalledTimes(1)
    expect(second).toHaveBeenCalledTimes(2)

    offSecond()
  })

  test('notifies nobody without complaining', () => {
    expect(() => notifyDocumentsChanged()).not.toThrow()
  })

  // A listener that tears itself down on the first notification is the shape
  // React's cleanup takes, and walking a set while it shrinks would skip the
  // listener after it.
  test('survives a listener that unsubscribes while being notified', () => {
    const later = vi.fn()
    const off = onDocumentsChanged(() => off())
    const offLater = onDocumentsChanged(later)

    notifyDocumentsChanged()
    expect(later).toHaveBeenCalledTimes(1)

    offLater()
  })
})
