import { useCallback, useState } from 'react'

/**
 * A boolean view preference in localStorage. Every access is guarded, because
 * localStorage throws outright when site data is blocked. Read in the
 * initializer rather than an effect, so the first paint is already remembered.
 */
export function useStoredFlag(
  key: string,
  defaultValue: boolean,
): [boolean, (next: boolean | ((current: boolean) => boolean)) => void] {
  const [value, setValue] = useState(() => {
    try {
      const stored = window.localStorage.getItem(key)
      return stored === null ? defaultValue : stored === 'true'
    } catch {
      return defaultValue
    }
  })

  const set = useCallback(
    (next: boolean | ((current: boolean) => boolean)) => {
      setValue((current) => {
        const resolved = typeof next === 'function' ? next(current) : next
        try {
          window.localStorage.setItem(key, String(resolved))
        } catch {
          // Preference is still applied for this session, just not remembered.
        }
        return resolved
      })
    },
    [key],
  )

  return [value, set]
}
