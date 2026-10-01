import { type SubmitEvent, useCallback, useState } from 'react'

import { useAppMeta } from '../hooks/useAppMeta'
import { useAsync } from '../hooks/useAsync'
import { useSettingsForm } from '../hooks/useSettingsForm'
import type { AppSettingsPatch } from '../lib/api/settings'
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
  TrashIcon,
} from '../components/ui'
import {
  CUSTOM_FIELD_TYPES,
  deleteCustomField,
  listCustomFields,
  saveCustomField,
  type CustomField,
  type CustomFieldInput,
  type CustomFieldType,
} from '../lib/api/customFields'
import { Combobox } from '../components/Combobox'
import { t } from '../i18n'

const CUSTOM_FIELD_TYPE_LABELS: Record<CustomFieldType, string> = {
  text: t('settingsProcessing.customFieldTypeText'),
  number: t('settingsProcessing.customFieldTypeNumber'),
  date: t('settingsProcessing.customFieldTypeDate'),
}

const CUSTOM_FIELD_TYPE_OPTIONS = CUSTOM_FIELD_TYPES.map((type) => ({
  value: type,
  label: CUSTOM_FIELD_TYPE_LABELS[type],
}))

const BLANK_CUSTOM_FIELD: CustomField = { id: '', name: '', type: 'text', description: '' }

/** One field, saved on its own: a row is a record, not part of the settings form. */
function CustomFieldRow({
  field,
  onSave,
  onRemove,
}: {
  field: CustomField
  onSave: (input: CustomFieldInput) => Promise<boolean>
  onRemove: () => void
}) {
  const [draft, setDraft] = useState<CustomFieldInput>(field)
  const [saving, setSaving] = useState(false)
  const dirty =
    !field.id ||
    draft.name.trim() !== field.name ||
    draft.type !== field.type ||
    draft.description.trim() !== field.description

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaving(true)
    if (await onSave(draft)) {
      setDraft({ ...draft, name: draft.name.trim(), description: draft.description.trim() })
    }
    setSaving(false)
  }

  return (
    <form
      onSubmit={onSubmit}
      className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_8rem_minmax(0,2fr)_auto_auto]"
    >
      <input
        className={inputClassName}
        aria-label={t('settingsProcessing.customFieldName')}
        placeholder={t('settingsProcessing.customFieldNamePlaceholder')}
        required
        maxLength={100}
        value={draft.name}
        onChange={(e) => setDraft({ ...draft, name: e.target.value })}
      />
      {/* Fixed once saved: the values already stored are of that type. */}
      <Combobox
        ariaLabel={t('settingsProcessing.customFieldType')}
        disabled={Boolean(field.id)}
        value={draft.type}
        options={CUSTOM_FIELD_TYPE_OPTIONS}
        onChange={(type) => setDraft({ ...draft, type: type as CustomFieldType })}
      />
      <input
        className={inputClassName}
        aria-label={t('settingsProcessing.customFieldDescription')}
        placeholder={t('settingsProcessing.customFieldDescriptionPlaceholder')}
        maxLength={500}
        value={draft.description}
        onChange={(e) => setDraft({ ...draft, description: e.target.value })}
      />
      <Button type="submit" size="sm" disabled={!dirty || saving || !draft.name.trim()}>
        {t('settingsProcessing.customFieldSave')}
      </Button>
      <button
        type="button"
        aria-label={t('settingsProcessing.customFieldRemove')}
        title={t('settingsProcessing.customFieldRemove')}
        disabled={saving}
        onClick={onRemove}
        className="self-center justify-self-start p-1 text-ink-soft transition-colors hover:text-madder disabled:opacity-50"
      >
        <TrashIcon />
      </button>
    </form>
  )
}

function CustomFieldsSection() {
  const { data: fields, error: loadError, reload } = useAsync(listCustomFields, [])
  const [adding, setAdding] = useState(false)
  const [error, setError] = useState('')
  const closeError = useCallback(() => setError(''), [])

  async function run(action: () => Promise<unknown>) {
    setError('')
    try {
      await action()
      await reload()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : t('customFieldsApi.saveFailed'))
      return false
    }
  }

  function remove(field: CustomField) {
    if (window.confirm(t('settingsProcessing.customFieldRemoveConfirm', { name: field.name }))) {
      void run(() => deleteCustomField(field.id))
    }
  }

  return (
    <section className={`${sectionClassName} mt-6 flex flex-col gap-2`}>
      <h2 className={sectionTitleClassName}>{t('settingsProcessing.customFields')}</h2>
      <p className={fieldHintClassName}>{t('settingsProcessing.customFieldsHint')}</p>
      {loadError && <p className="text-sm text-madder">{loadError}</p>}
      {(fields ?? []).map((field) => (
        <CustomFieldRow
          key={field.id}
          field={field}
          onSave={(input) => run(() => saveCustomField(field.id, input))}
          onRemove={() => remove(field)}
        />
      ))}
      {adding ? (
        <CustomFieldRow
          field={BLANK_CUSTOM_FIELD}
          onSave={async (input) => {
            const saved = await run(() => saveCustomField('', input))
            if (saved) setAdding(false)
            return saved
          }}
          onRemove={() => setAdding(false)}
        />
      ) : (
        <div>
          <Button variant="secondary" size="sm" onClick={() => setAdding(true)}>
            {t('settingsProcessing.customFieldAdd')}
          </Button>
        </div>
      )}
      <ResultDialog error={error} success="" onClose={closeError} />
    </section>
  )
}

export function SettingsProcessingPage() {
  return (
    <>
      <ProcessingSettingsForm />
      <CustomFieldsSection />
    </>
  )
}

/**
 * A managed tenant keeps everything here but the two timeouts, which its host
 * re-applies from the environment on every boot.
 */
function ProcessingSettingsForm() {
  // unknown/failed meta counts as managed; see AppMeta.managed
  const { managed } = useAppMeta()
  const timeoutsEditable = managed === false
  const { form, loading, error, success, saving, updateField, save, setError, closeResult } =
    useSettingsForm((settings) => ({
      ocr_timeout_sec: String(settings.ocr_timeout_sec),
      openai_timeout_sec: String(settings.openai_timeout_sec),
      processing_result_language: settings.processing_result_language,
      deep_search_languages: settings.deep_search_languages,
      extraction_rules: settings.extraction_rules,
      always_require_review: settings.always_require_review,
      embedding_model: settings.embedding_model,
    }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return

    let timeouts: AppSettingsPatch = {}
    if (timeoutsEditable) {
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
      timeouts = { ocr_timeout_sec: ocrTimeout, openai_timeout_sec: openAITimeout }
    }

    await save({
      ...timeouts,
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
          {timeoutsEditable && (
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
        <div className="mt-4">
          <SaveSettingsButton saving={saving} />
        </div>
      </section>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </form>
  )
}
