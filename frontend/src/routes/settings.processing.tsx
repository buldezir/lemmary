import { type SubmitEvent, useId } from 'react'

import { Combobox } from '../components/Combobox'
import { useAppMeta } from '../hooks/useAppMeta'
import { useSettingsForm } from '../hooks/useSettingsForm'
import type { AppSettingsPatch } from '../lib/api/settings'
import {
  ResultDialog,
  SaveSettingsButton,
  SettingsLoading,
} from '../components/settings/SettingsFeedback'
import {
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'
import { t } from '../i18n'

const subTitleClassName = 'mb-3 mt-6 text-sm font-semibold text-ink'

const timeZoneOptions = [...new Set(['UTC', ...Intl.supportedValuesOf('timeZone')])].map((zone) => ({
  value: zone,
  label: zone.replaceAll('_', ' '),
}))

/**
 * A managed tenant keeps everything here but the two timeouts, which its host
 * re-applies from the environment on every boot, and the processing hours,
 * which are about an AI server its host runs.
 */
export function SettingsProcessingPage() {
  // unknown/failed meta counts as managed; see AppMeta.managed
  const { managed } = useAppMeta()
  const selfHosted = managed === false
  const timeZoneId = useId()
  const { form, loading, error, success, saving, updateField, save, setError, closeResult } =
    useSettingsForm((settings) => ({
      ocr_timeout_sec: String(settings.ocr_timeout_sec),
      openai_timeout_sec: String(settings.openai_timeout_sec),
      processing_result_language: settings.processing_result_language,
      deep_search_languages: settings.deep_search_languages,
      extraction_rules: settings.extraction_rules,
      always_require_review: settings.always_require_review,
      embedding_model: settings.embedding_model,
      processing_window_enabled: settings.processing_window_enabled,
      processing_window_start: settings.processing_window_start || '22:00',
      processing_window_end: settings.processing_window_end || '07:00',
      processing_timezone:
        settings.processing_timezone || Intl.DateTimeFormat().resolvedOptions().timeZone,
      processing_ping_enabled: settings.processing_ping_enabled,
    }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return

    let hostOwned: AppSettingsPatch = {}
    if (selfHosted) {
      const ocrTimeout = Number(form.ocr_timeout_sec)
      const openAITimeout = Number(form.openai_timeout_sec)
      if (!Number.isFinite(ocrTimeout) || ocrTimeout <= 0) {
        setError(t('settingsProcessing.ocrTimeoutInvalid'))
        return
      }
      if (!Number.isFinite(openAITimeout) || openAITimeout <= 0) {
        setError(t('settingsProcessing.aiTimeoutInvalid'))
        return
      }
      hostOwned = {
        ocr_timeout_sec: ocrTimeout,
        openai_timeout_sec: openAITimeout,
        processing_window_enabled: form.processing_window_enabled,
        processing_window_start: form.processing_window_start,
        processing_window_end: form.processing_window_end,
        processing_timezone: form.processing_timezone,
        processing_ping_enabled: form.processing_ping_enabled,
      }
    }

    await save({
      ...hostOwned,
      processing_result_language: form.processing_result_language,
      deep_search_languages: form.deep_search_languages,
      extraction_rules: form.extraction_rules,
      always_require_review: form.always_require_review,
    })
  }

  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <form onSubmit={onSubmit}>
      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>{t('settingsProcessing.title')}</h2>
        {/* In a panel of its own: it is the only setting here that decides
            where a finished document goes. */}
        <div className="mb-4 rounded-xs border border-line-strong bg-bright p-4">
          <label className="flex items-center gap-2.5 text-sm font-medium text-ink">
            <input
              type="checkbox"
              className="h-4 w-4 accent-oxblood"
              checked={form.always_require_review}
              onChange={(e) => updateField('always_require_review', e.target.checked)}
            />
            {t('settingsProcessing.alwaysReview')}
          </label>
          <p className={`${fieldHintClassName} mt-2`}>
            {t('settingsProcessing.alwaysReviewHint')}
          </p>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          {selfHosted && (
            <>
              <div className={labelClassName}>
                <label className={labelClassName}>
                  <span className={labelTextClassName}>{t('settingsProcessing.ocrTimeout')}</span>
                  <input
                    type="number"
                    min={1}
                    className={inputClassName}
                    value={form.ocr_timeout_sec}
                    onChange={(e) => updateField('ocr_timeout_sec', e.target.value)}
                  />
                </label>
                <p className={fieldHintClassName}>
                  {t('settingsProcessing.ocrTimeoutHint')}
                </p>
              </div>
              <div className={labelClassName}>
                <label className={labelClassName}>
                  <span className={labelTextClassName}>{t('settingsProcessing.aiTimeout')}</span>
                  <input
                    type="number"
                    min={1}
                    className={inputClassName}
                    value={form.openai_timeout_sec}
                    onChange={(e) => updateField('openai_timeout_sec', e.target.value)}
                  />
                </label>
                <p className={fieldHintClassName}>
                  {t('settingsProcessing.aiTimeoutHint')}
                </p>
              </div>
            </>
          )}
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>{t('settingsProcessing.resultLanguage')}</span>
              <input
                className={inputClassName}
                placeholder={t('settingsProcessing.resultLanguagePlaceholder')}
                value={form.processing_result_language}
                onChange={(e) => updateField('processing_result_language', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              {t('settingsProcessing.resultLanguageHint')}
            </p>
          </div>
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>{t('settingsProcessing.deepSearchLanguages')}</span>
              <input
                className={inputClassName}
                placeholder={t('settingsProcessing.deepSearchLanguagesPlaceholder')}
                value={form.deep_search_languages}
                onChange={(e) => updateField('deep_search_languages', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              {form.embedding_model.trim() !== ''
                ? t('settingsProcessing.deepSearchLanguagesEmbeddingHint')
                : t('settingsProcessing.deepSearchLanguagesHint')}
            </p>
          </div>
          <div className={`${labelClassName} sm:col-span-2`}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>{t('settingsProcessing.extractionRules')}</span>
              <textarea
                rows={6}
                className={inputClassName}
                placeholder={t('settingsProcessing.extractionRulesPlaceholder')}
                value={form.extraction_rules}
                onChange={(e) => updateField('extraction_rules', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              {t('settingsProcessing.extractionRulesHint')}
            </p>
          </div>
        </div>
        {selfHosted && (
          <>
            <h3 className={subTitleClassName}>{t('settingsProcessing.scheduleTitle')}</h3>
            <p className={`${fieldHintClassName} mb-4`}>{t('settingsProcessing.scheduleHint')}</p>
            <label className="flex items-center gap-2.5 text-sm font-medium text-ink">
              <input
                type="checkbox"
                className="h-4 w-4 accent-oxblood"
                checked={form.processing_window_enabled}
                onChange={(e) => updateField('processing_window_enabled', e.target.checked)}
              />
              {t('settingsProcessing.windowEnabled')}
            </label>
            <p className={`${fieldHintClassName} mt-1`}>{t('settingsProcessing.windowHint')}</p>
            {form.processing_window_enabled && (
              <div className="mt-3 grid gap-4 sm:grid-cols-3">
                <label className={labelClassName}>
                  <span className={labelTextClassName}>{t('settingsProcessing.windowStart')}</span>
                  <input
                    type="time"
                    required
                    className={inputClassName}
                    value={form.processing_window_start}
                    onChange={(e) => updateField('processing_window_start', e.target.value)}
                  />
                </label>
                <label className={labelClassName}>
                  <span className={labelTextClassName}>{t('settingsProcessing.windowEnd')}</span>
                  <input
                    type="time"
                    required
                    className={inputClassName}
                    value={form.processing_window_end}
                    onChange={(e) => updateField('processing_window_end', e.target.value)}
                  />
                </label>
                <div className={labelClassName}>
                  <label htmlFor={timeZoneId} className={labelTextClassName}>
                    {t('settingsProcessing.timezone')}
                  </label>
                  <Combobox
                    id={timeZoneId}
                    value={form.processing_timezone}
                    options={timeZoneOptions}
                    onChange={(next) => updateField('processing_timezone', next)}
                  />
                </div>
              </div>
            )}
            <label className="mt-4 flex items-center gap-2.5 text-sm font-medium text-ink">
              <input
                type="checkbox"
                className="h-4 w-4 accent-oxblood"
                checked={form.processing_ping_enabled}
                onChange={(e) => updateField('processing_ping_enabled', e.target.checked)}
              />
              {t('settingsProcessing.pingEnabled')}
            </label>
            <p className={`${fieldHintClassName} mt-1`}>{t('settingsProcessing.pingHint')}</p>
          </>
        )}
        <div className="mt-4">
          <SaveSettingsButton saving={saving} />
        </div>
      </section>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </form>
  )
}
