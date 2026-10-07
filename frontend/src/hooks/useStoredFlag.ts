import { useCallback, useState } from 'react'

/**
 * A view preference in localStorage. Every access is guarded, because
 * localStorage throws outright when site data is blocked. Read in the
 * initializer rather than an effect, so the first paint is already remembered.
 * `parse` returns undefined for a stored value it does not accept, which falls
 * back to the default.
 */
export function useStoredValue<T extends boolean | number>(
  key: string,
  defaultValue: T,
  parse: (stored: string) => T | undefined,
): [T, (next: T | ((current: T) => T)) => void] {
  const [value, setValue] = useState(() => {
    try {
      const stored = window.localStorage.getItem(key)
      return (stored === null ? undefined : parse(stored)) ?? defaultValue
    } catch {
      return defaultValue
    }
  })

  const set = useCallback(
    (next: T | ((current: T) => T)) => {
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

export function useStoredFlag(key: string, defaultValue: boolean) {
  return useStoredValue<boolean>(key, defaultValue, (stored) => stored === 'true')
}
