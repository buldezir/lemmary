import { DEFAULT_ACCENT, DEFAULT_APP_NAME, getAppMeta, type AppMeta } from '../lib/api/meta'
import { useAsync } from './useAsync'

const fallbackMeta: AppMeta = { appName: DEFAULT_APP_NAME, accent: DEFAULT_ACCENT }

export function useAppMeta(): AppMeta {
  // getAppMeta never throws; it falls back to defaults internally.
  const { data } = useAsync(getAppMeta, [])
  return data ?? fallbackMeta
}
