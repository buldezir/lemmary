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

export function SettingsAppearancePage() {
  const { form, loading, error, success, saving, updateField, save, setError, closeResult } =
    useSettingsForm((settings) => ({
      app_name: settings.app_name,
      // The color input has no empty state, so it shows the accent actually in
      // force. Saving an untouched form therefore writes the default down,
      // which is what it was already resolving to.
      accent: settings.accent || DEFAULT_ACCENT,
    }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return
    if (form.app_name.trim() === '') {
      setError('Application name is required')
      return
    }
    if (!/^#[0-9a-fA-F]{6}$/.test(form.accent)) {
      setError('Accent color must be a hex value like #6e2620')
      return
    }
    await save({ app_name: form.app_name.trim(), accent: form.accent })
  }

  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <form onSubmit={onSubmit}>
      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>Appearance</h2>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Application name</span>
              <input
                className={inputClassName}
                value={form.app_name}
                onChange={(e) => updateField('app_name', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              Shown in the header and on the sign-in page, and used as the sender name on mail
              and in passkey prompts.
            </p>
          </div>
          <div className={labelClassName}>
            <span className={labelTextClassName}>Accent color</span>
            <div className="flex items-center gap-2">
              <input
                type="color"
                aria-label="Accent color"
                className="h-9 w-12 cursor-pointer border border-line bg-surface p-1"
                value={form.accent}
                onChange={(e) => updateField('accent', e.target.value)}
              />
              <input
                className={`${inputClassName} font-mono`}
                aria-label="Accent color hex"
                spellCheck={false}
                value={form.accent}
                onChange={(e) => updateField('accent', e.target.value.trim())}
              />
              <Button
                variant="secondary"
                size="sm"
                onClick={() => updateField('accent', DEFAULT_ACCENT)}
              >
                Reset
              </Button>
            </div>
            <p className={fieldHintClassName}>
              Colors the logo mark and the accents around it. Pick a swatch or paste a hex value
              such as {DEFAULT_ACCENT}.
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
