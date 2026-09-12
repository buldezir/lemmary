import { useEffect, useRef } from 'react'
import {
  DEFAULT_ACCENT,
  DEFAULT_APP_NAME,
  getAppMeta,
  onAppMetaChanged,
  type AppMeta,
} from '../lib/api/meta'
import { useAsync } from './useAsync'

// aiManaged omitted: unknown must not default to "not managed". See AppMeta.
const fallbackMeta: AppMeta = {
  appName: DEFAULT_APP_NAME,
  accent: DEFAULT_ACCENT,
}

/**
 * metaLoaded separates "not answered yet" from the two answers that both read
 * as managed. Without it a reader cannot tell an in-flight request from a known
 * `aiManaged: true`, and anything that *states* which of the two it is -- rather
 * than just offering less -- says the wrong thing on the first render.
 */
export function useAppMeta(): AppMeta & { metaLoaded: boolean } {
  // getAppMeta never throws; it falls back to defaults internally.
  const { data, reload } = useAsync(getAppMeta, [])
  const reloadRef = useRef(reload)
  useEffect(() => {
    reloadRef.current = reload
  })

  // Saving Settings invalidates the cache, and every mounted reader has to hear
  // about it: the header offers the Inbox only where review is required, and it
  // does not remount when an admin flips that.
  useEffect(() => onAppMetaChanged(() => void reloadRef.current()), [])

  // getAppMeta resolves with the fallback rather than rejecting, so data being
  // set covers a failed request too: it answered, and the answer is "unknown".
  return { ...(data ?? fallbackMeta), metaLoaded: data !== null }
}
