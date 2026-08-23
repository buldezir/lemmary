import { useCallback, useEffect, useRef, useState } from 'react'

export type AsyncResult<T> = {
  data: T | null
  loading: boolean
  error: string
  /** Re-runs the loader without toggling `loading` (background refresh). */
  reload: () => Promise<void>
}

/**
 * Runs an async loader on mount and whenever `deps` change, guarding against
 * responses that arrive after the inputs changed.
 */
export function useAsync<T>(load: () => Promise<T>, deps: readonly unknown[]): AsyncResult<T> {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  // Bumped whenever the inputs change; a settled promise from an older
  // generation must not write its result over the newer one.
  const generation = useRef(0)
  const loadRef = useRef(load)
  useEffect(() => {
    loadRef.current = load
  })

  const run = useCallback(async (isReload: boolean) => {
    const current = ++generation.current
    if (!isReload) {
      setLoading(true)
    }
    try {
      const next = await loadRef.current()
      if (generation.current !== current) {
        return
      }
      setData(next)
      setError('')
    } catch (err) {
      if (generation.current !== current) {
        return
      }
      setError(err instanceof Error ? err.message : 'Failed to load')
    } finally {
      if (generation.current === current && !isReload) {
        setLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    // The microtask keeps the load's setState calls out of the effect's
    // synchronous body, so a load can never cascade renders.
    let cancelled = false
    void Promise.resolve().then(() => {
      if (!cancelled) {
        void run(false)
      }
    })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deps are the caller's loader inputs
  }, deps)

  const reload = useCallback(() => run(true), [run])

  return { data, loading, error, reload }
}
