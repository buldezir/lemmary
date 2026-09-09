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

export function useAppMeta(): AppMeta {
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

  return data ?? fallbackMeta
}
