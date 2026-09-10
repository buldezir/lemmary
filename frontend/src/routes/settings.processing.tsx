import { type SubmitEvent } from 'react'

import { useSettingsForm } from '../hooks/useSettingsForm'
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

/**
 * None of these is a provider or a model, so a managed tenant keeps them: the
 * environment seeds them on the first boot and never touches them again, in
 * either mode.
 */
export function SettingsProcessingPage() {
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

    const ocrTimeout = Number(form.ocr_timeout_sec)
    const openAITimeout = Number(form.openai_timeout_sec)
    if (!Number.isFinite(ocrTimeout) || ocrTimeout <= 0) {
      setError('OCR timeout must be a positive number')
      return
    }
    if (!Number.isFinite(openAITimeout) || openAITimeout <= 0) {
      setError('AI timeout must be a positive number')
      return
    }

    await save({
      ocr_timeout_sec: ocrTimeout,
      openai_timeout_sec: openAITimeout,
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
        <h2 className={sectionTitleClassName}>Processing</h2>
        {/*
          Above the timeouts, and in a panel of its own: it is the only setting
          here that decides where a finished document goes, and it was the
          easiest to miss at the foot of a two-column grid.
        */}
        <div className="mb-4 rounded-xs border border-line-strong bg-bright p-4">
          <label className="flex items-center gap-2.5 text-sm font-medium text-ink">
            <input
              type="checkbox"
              className="h-4 w-4 accent-oxblood"
              checked={form.always_require_review}
              onChange={(e) => updateField('always_require_review', e.target.checked)}
            />
            Always require review for new documents
          </label>
          <p className={`${fieldHintClassName} mt-2`}>
            Every document the AI extracts metadata for waits in the Inbox, however confident
            the extraction was &mdash; reprocessed documents included. Nothing completes but by
            your saying so. Off, only low-confidence extractions and possible duplicates land
            there.
          </p>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>OCR timeout (seconds)</span>
              <input
                type="number"
                min={1}
                className={inputClassName}
                value={form.ocr_timeout_sec}
                onChange={(e) => updateField('ocr_timeout_sec', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              How long one OCR call may take before the step fails.
            </p>
          </div>
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>AI timeout (seconds)</span>
              <input
                type="number"
                min={1}
                className={inputClassName}
                value={form.openai_timeout_sec}
                onChange={(e) => updateField('openai_timeout_sec', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              How long one extraction, chat, search or split-detection request may take.
            </p>
          </div>
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Result language (ISO 639-1)</span>
              <input
                className={inputClassName}
                placeholder="e.g. en"
                value={form.processing_result_language}
                onChange={(e) => updateField('processing_result_language', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              Also stores the title, purpose, summary, type, correspondent and tags
              translated into this language. Leave empty to keep only the document&rsquo;s own
              language.
            </p>
          </div>
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Deep search languages</span>
              <input
                className={inputClassName}
                placeholder="e.g. de,en,uk"
                value={form.deep_search_languages}
                onChange={(e) => updateField('deep_search_languages', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              {form.embedding_model.trim() !== ''
                ? 'With an embedding model configured, one search already reaches documents in every language; this list is only used when Deep Search falls back to keyword search.'
                : 'Languages deep search translates keywords into, so a German invoice is found by an English question. Leave empty to search only in the language of the question.'}
            </p>
          </div>
          <div className={`${labelClassName} sm:col-span-2`}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Extra extraction rules</span>
              <textarea
                rows={6}
                className={inputClassName}
                placeholder={
                  'e.g. Treat "Rechnung" as the document type Invoice.\n' +
                  'Tag every insurance document with the policy number.'
                }
                value={form.extraction_rules}
                onChange={(e) => updateField('extraction_rules', e.target.value)}
              />
            </label>
            <p className={fieldHintClassName}>
              Your own instructions, added to the prompt that reads metadata out of a document
              &mdash; house conventions for titles, types, correspondents or tags. They cannot
              change which fields are stored. Applies to documents processed or reprocessed from
              now on; leave empty for the built-in prompt alone.
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
