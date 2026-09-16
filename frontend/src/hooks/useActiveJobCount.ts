import { useEffect, useRef } from 'react'
import { pb } from '../lib/pb'
import { getActiveJobCounts } from '../lib/api/maintenance'
import { useAsync } from './useAsync'

const debounceMs = 300

/**
 * For the header's Activity badge. Null while unknown and on failure, so the
 * badge hides rather than claiming an idle queue. Same shape as useInboxCount,
 * debounced because a bulk upload turns one answer into twenty events.
 */
export function useActiveJobCount(): number | null {
  const { data, reload } = useAsync(() => getActiveJobCounts(), [])
  const reloadRef = useRef(reload)
  useEffect(() => {
    reloadRef.current = reload
  })

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    function schedule() {
      clearTimeout(timer)
      timer = setTimeout(() => void reloadRef.current(), debounceMs)
    }

    let unsubscribe: (() => void) | undefined
    let cancelled = false
    void pb
      .collection('processing_jobs')
      .subscribe('*', schedule)
      .then((off) => {
        if (cancelled) {
          void off()
          return
        }
        unsubscribe = off
      })
      .catch(() => {
        // No realtime: the count is still correct as of the last load, and the
        // Activity page has its own interval fallback.
      })

    return () => {
      cancelled = true
      clearTimeout(timer)
      void unsubscribe?.()
    }
  }, [])

  return data === null ? null : data.pending + data.running
}
