import { type SubmitEvent } from 'react'
import { Link } from '@tanstack/react-router'

import { useAppMeta } from '../hooks/useAppMeta'
import { useSettingsForm } from '../hooks/useSettingsForm'
import {
  ManagedByHostNotice,
  ResultDialog,
  SaveSettingsButton,
  SettingsLoading,
} from '../components/settings/SettingsFeedback'
import {
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'
import { t, tNode } from '../i18n'

export function SettingsDuplicatesPage() {
  // unknown/failed meta counts as managed; see AppMeta.aiManaged
  const { aiManaged, metaLoaded } = useAppMeta()
  const { form, loading, error, success, saving, updateField, save, setError, closeResult } =
    useSettingsForm((settings) => ({
      near_duplicate_detection_enabled: settings.near_duplicate_detection_enabled,
      near_duplicate_threshold: String(settings.near_duplicate_threshold ?? 0.92),
    }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return

    const threshold = Number(form.near_duplicate_threshold)
    if (!Number.isFinite(threshold) || threshold <= 0 || threshold > 1) {
      setError(t('settingsDuplicates.thresholdInvalid'))
      return
    }

    await save({
      near_duplicate_detection_enabled: form.near_duplicate_detection_enabled,
      near_duplicate_threshold: threshold,
    })
  }

  // See the AI tab: in-flight meta is not a licence to name an owner.
  if (!metaLoaded) return <SettingsLoading error="" />
  if (aiManaged !== false) return <ManagedByHostNotice />
  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <form onSubmit={onSubmit}>
      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>{t('settingsDuplicates.title')}</h2>
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="flex items-center gap-2 text-sm text-ink-muted sm:col-span-2">
            <input
              type="checkbox"
              checked={form.near_duplicate_detection_enabled}
              onChange={(e) => updateField('near_duplicate_detection_enabled', e.target.checked)}
            />
            {t('settingsDuplicates.enable')}
          </label>
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('settingsDuplicates.threshold')}</span>
            <input
              type="number"
              min={0.01}
              max={1}
              step={0.01}
              className={inputClassName}
              value={form.near_duplicate_threshold}
              onChange={(e) => updateField('near_duplicate_threshold', e.target.value)}
            />
          </label>
        </div>
        <p className="mt-3 text-xs text-ink-soft">
          {tNode('settingsDuplicates.hint', {
            link: (
              <Link to="/maintenance" className="underline hover:text-oxblood">
                {t('settingsDuplicates.maintenanceLink')}
              </Link>
            ),
          })}
        </p>
        <div className="mt-4">
          <SaveSettingsButton saving={saving} />
        </div>
      </section>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </form>
  )
}
