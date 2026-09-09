import { useEffect, useRef } from 'react'
import { pb } from '../lib/pb'
import { countInboxDocuments } from '../lib/api/documents'
import { onDocumentsChanged } from '../lib/documentEvents'
import { useAsync } from './useAsync'

const debounceMs = 300

/**
 * How many documents the pipeline has not finished with, for the header's Inbox
 * badge. Null while unknown and on failure, so the badge hides rather than
 * claiming an empty Inbox.
 *
 * Call it once, in the header: the two header layouts render from one
 * component precisely so this is one request rather than one per link.
 */
export function useInboxCount(): number | null {
  const { data, reload } = useAsync(() => countInboxDocuments(), [])
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
