import { type SubmitEvent } from 'react'

import { DEFAULT_ACCENT } from '../lib/api/meta'
import { useSettingsForm } from '../hooks/useSettingsForm'
import {
  ResultDialog,
  SaveSettingsButton,
  SettingsLoading,
} from '../components/settings/SettingsFeedback'
import {
  Button,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'
import { t } from '../i18n'

export function SettingsAppearancePage() {
  const { form, loading, error, success, saving, updateField, save, setError, closeResult } =
    useSettingsForm((settings) => ({
      app_name: settings.app_name,
      // The color input has no empty state, so it shows the accent actually in
      // force, and saving an untouched form writes the default down.
      accent: settings.accent || DEFAULT_ACCENT,
    }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return
    if (form.app_name.trim() === '') {
      setError(t('settingsIndex.nameRequired'))
      return
    }
    if (!/^#[0-9a-fA-F]{6}$/.test(form.accent)) {
      setError(t('settingsIndex.accentInvalid'))
      return
    }
    await save({ app_name: form.app_name.trim(), accent: form.accent })
  }

  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <form onSubmit={onSubmit}>
      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>{t('settingsIndex.title')}</h2>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>{t('settingsIndex.appName')}</span>
              <input
                className={inputClassName}
                value={form.app_name}
                onChange={(e) => updateField('app_name', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              {t('settingsIndex.appNameHint')}
            </p>
          </div>
          <div className={labelClassName}>
            <span className={labelTextClassName}>{t('settingsIndex.accent')}</span>
            <div className="flex items-center gap-2">
              <input
                type="color"
                aria-label={t('settingsIndex.accent')}
                className="h-9 w-12 cursor-pointer border border-line bg-surface p-1"
                value={form.accent}
                onChange={(e) => updateField('accent', e.target.value)}
              />
              <input
                className={`${inputClassName} font-mono`}
                aria-label={t('settingsIndex.accentHex')}
                spellCheck={false}
                value={form.accent}
                onChange={(e) => updateField('accent', e.target.value.trim())}
              />
              <Button
                variant="secondary"
                size="sm"
                onClick={() => updateField('accent', DEFAULT_ACCENT)}
              >
                {t('settingsIndex.reset')}
              </Button>
            </div>
            <p className={fieldHintClassName}>
              {t('settingsIndex.accentHint', { accent: DEFAULT_ACCENT })}
            </p>
          </div>
        </div>
        <div className="mt-4">
          <SaveSettingsButton saving={saving} />
        </div>
      </section>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </form>
  )
}
