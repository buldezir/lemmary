import { DEFAULT_ACCENT, DEFAULT_APP_NAME, getAppMeta, type AppMeta } from '../lib/api/meta'
import { useAsync } from './useAsync'

// aiManaged is deliberately absent: while the request is in flight nobody knows
// who owns AI configuration, and saying "not managed" for that moment is what
// makes the operator-owned sections flash into view before vanishing.
const fallbackMeta: AppMeta = {
  appName: DEFAULT_APP_NAME,
  accent: DEFAULT_ACCENT,
}

export function useAppMeta(): AppMeta {
  // getAppMeta never throws; it falls back to defaults internally.
  const { data } = useAsync(getAppMeta, [])
  return data ?? fallbackMeta
}
