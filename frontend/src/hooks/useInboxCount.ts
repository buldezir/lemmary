import { useEffect, useRef } from 'react'
import { pb } from '../lib/pb'
import { countDocumentsNeedingReview } from '../lib/api/documents'
import { onDocumentsChanged } from '../lib/documentEvents'
import { useAsync } from './useAsync'

/** How long to sit on a burst of changes before counting again. */
const debounceMs = 300

/**
 * How many documents are waiting for review, for the header's Inbox badge.
 *
 * Null while unknown, and null again if the count fails: a badge that hides is
 * honest about not knowing, whereas a zero would claim the Inbox is empty.
 *
 * Call this once per app, in the header -- the count is a request, and the two
 * header layouts render the same nav data from one component precisely so
 * there is one of it.
 */
export function useInboxCount(): number | null {
  const { data, reload } = useAsync(() => countDocumentsNeedingReview(), [])
  const reloadRef = useRef(reload)
  useEffect(() => {
    reloadRef.current = reload
  })

  useEffect(() => {
    // Debounced because the events arrive in bursts: marking a page of twelve
    // documents reviewed is twelve realtime events for one answer.
    let timer: ReturnType<typeof setTimeout> | undefined
    function schedule() {
      clearTimeout(timer)
      timer = setTimeout(() => void reloadRef.current(), debounceMs)
    }

    const offLocal = onDocumentsChanged(schedule)
    // Realtime covers changes made elsewhere -- another tab, another device,
    // the worker finishing a document. It is optional; the local event is what
    // keeps the badge right when it is unavailable.
    let unsubscribe: (() => void) | undefined
    let cancelled = false
    void pb
      .collection('documents')
      .subscribe('*', schedule)
      .then((off) => {
        if (cancelled) {
          void off()
          return
        }
        unsubscribe = off
      })
      .catch(() => {
        // No realtime: the local event still fires on our own writes.
      })

    return () => {
      cancelled = true
      clearTimeout(timer)
      offLocal()
      void unsubscribe?.()
    }
  }, [])

  return data
}
