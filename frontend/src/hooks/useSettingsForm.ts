import { useCallback, useEffect, useRef, useState } from 'react'

import {
  getAppSettings,
  updateAppSettings,
  type AppSettings,
  type AppSettingsPatch,
} from '../lib/api/settings'
import { useAsync } from './useAsync'

/**
 * One Settings tab's slice of app_settings. Every PATCH field is optional on
 * the server, so a tab naming only its own fields leaves the rest untouched,
 * which is what lets the tabs live on separate routes.
 */
export function useSettingsForm<T extends object>(pick: (settings: AppSettings) => T) {
  const { data, loading, error: loadError } = useAsync(getAppSettings, [])
  const [form, setForm] = useState<T | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  // Held in a ref because every caller passes an inline lambda: in the effect's
  // deps it would re-seed the form each render and lose what was half-typed.
  const pickRef = useRef(pick)
  useEffect(() => {
    pickRef.current = pick
  })

  useEffect(() => {
    if (data) setForm(pickRef.current(data))
  }, [data])

  // Takes a function as well as an object because the provider picker fires
  // onProviderChange and onModelChange back to back: a patch built from the
  // render's `form` would undo the first of the two.
  const updateFields = useCallback((patch: Partial<T> | ((current: T) => Partial<T>)) => {
    setForm((current) => {
      if (!current) return current
      return { ...current, ...(typeof patch === 'function' ? patch(current) : patch) }
    })
    setSuccess('')
  }, [])

  const updateField = useCallback(
    <K extends keyof T>(key: K, value: T[K]) => {
      updateFields({ [key]: value } as unknown as Partial<T>)
    },
    [updateFields],
  )

  const save = useCallback(async (patch: AppSettingsPatch) => {
    try {
      setSaving(true)
      setError('')
      setSuccess('')
      const settings = await updateAppSettings(patch)
      setForm(pickRef.current(settings))
      setSuccess('Settings saved. Runtime reloaded.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings')
    } finally {
      setSaving(false)
    }
  }, [])

  const closeResult = useCallback(() => {
    setError('')
    setSuccess('')
  }, [])

  return {
    form,
    loading,
    /** Set by a tab that refuses its own values before calling save. */
    error: error || loadError,
    success,
    saving,
    updateField,
    updateFields,
    save,
    setError,
    setSuccess,
    closeResult,
  }
}
