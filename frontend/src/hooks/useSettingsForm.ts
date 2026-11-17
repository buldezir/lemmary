import { t } from '../i18n'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useBlocker } from '@tanstack/react-router'

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
  // The form as last loaded or saved: what `dirty` compares against.
  const [saved, setSaved] = useState<T | null>(null)
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
    if (!data) return
    const picked = pickRef.current(data)
    setForm(picked)
    setSaved(picked)
  }, [data])

  const dirty = form !== null && JSON.stringify(form) !== JSON.stringify(saved)
  // A tab is a route, so leaving it unmounts the form and whatever was typed.
  useBlocker({
    shouldBlockFn: () => !window.confirm(t('useSettingsForm.leaveConfirm')),
    enableBeforeUnload: dirty,
    disabled: !dirty,
  })

  // Takes a function as well as an object because the provider picker fires
  // onProviderChange and onModelChange back to back: a patch built from the
  // render's `form` would undo the first of the two.
  const updateFields = useCallback((patch: Partial<T> | ((current: T) => Partial<T>)) => {
    setForm((current) => {
      if (!current) return current
      return { ...current, ...(typeof patch === 'function' ? patch(current) : patch) }
    })
    setError('')
    setSuccess('')
  }, [])

  const updateField = useCallback(
    <K extends keyof T>(key: K, value: T[K]) => {
      updateFields({ [key]: value } as unknown as Partial<T>)
    },
    [updateFields],
  )

  const savingRef = useRef(false)
  const save = useCallback(async (patch: AppSettingsPatch) => {
    if (savingRef.current) return
    try {
      savingRef.current = true
      setSaving(true)
      setError('')
      setSuccess('')
      const settings = await updateAppSettings(patch)
      const picked = pickRef.current(settings)
      setForm(picked)
      setSaved(picked)
      setSuccess(t('useSettingsForm.saved'))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('settingsApi.saveFailed'))
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  }, [])

  return {
    form,
    loading,
    /** Set by a tab that refuses its own values before calling save. */
    error: error || loadError,
    /** What SaveSettingsButton shows beside itself. */
    status: { saving, dirty, error, success },
    updateField,
    updateFields,
    save,
    setError,
  }
}
