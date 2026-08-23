import { type SubmitEvent, useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  createAIProvider,
  deleteAIProvider,
  isLLMProvider,
  listAIProviders,
  sdkLabel,
  updateAIProvider,
  SDK_DEFAULT_BASE,
  SDK_OPTIONS,
  type AIProvider,
  type ProviderSDK,
} from '../lib/api/providers'
import { getAppSettings, updateAppSettings, type AppSettings } from '../lib/api/settings'
import { ProviderModelFields } from '../components/ProviderModelFields'
import {
  Button,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'

function SaveSettingsButton({ saving }: { saving: boolean }) {
  return (
    <div>
      <Button type="submit" disabled={saving}>
        {saving ? 'Saving...' : 'Save settings'}
      </Button>
    </div>
  )
}

type FormState = {
  ocr_provider_id: string
  ocr_model: string
  extract_provider_id: string
  extract_model: string
  chat_provider_id: string
  chat_model: string
  search_provider_id: string
  search_model: string
  ocr_timeout_sec: string
  processing_result_language: string
  deep_search_languages: string
  openai_timeout_sec: string
  worker_timeout_sec: string
  worker_max_retries: string
  extraction_prompt_version: string
  near_duplicate_detection_enabled: boolean
  near_duplicate_threshold: string
}

function formFromSettings(settings: AppSettings): FormState {
  return {
    ocr_provider_id: settings.ocr_provider_id,
    ocr_model: settings.ocr_model,
    extract_provider_id: settings.extract_provider_id,
    extract_model: settings.extract_model,
    chat_provider_id: settings.chat_provider_id,
    chat_model: settings.chat_model,
    search_provider_id: settings.search_provider_id,
    search_model: settings.search_model,
    ocr_timeout_sec: String(settings.ocr_timeout_sec),
    processing_result_language: settings.processing_result_language,
    deep_search_languages: settings.deep_search_languages,
    openai_timeout_sec: String(settings.openai_timeout_sec),
    worker_timeout_sec: String(settings.worker_timeout_sec),
    worker_max_retries: String(settings.worker_max_retries),
    extraction_prompt_version: settings.extraction_prompt_version,
    near_duplicate_detection_enabled: settings.near_duplicate_detection_enabled,
    near_duplicate_threshold: String(settings.near_duplicate_threshold ?? 0.92),
  }
}

type ProviderDraft = {
  sdk: ProviderSDK
  alias: string
  base_url: string
  api_key: string
}

function emptyDraft(sdk: ProviderSDK = 'openai'): ProviderDraft {
  return { sdk, alias: '', base_url: SDK_DEFAULT_BASE[sdk], api_key: '' }
}

// Admin access is enforced by the route's beforeLoad guard, so this page can
// assume the caller is an admin.
export function SettingsPage() {
  const [form, setForm] = useState<FormState | null>(null)
  const [providers, setProviders] = useState<AIProvider[]>([])
  const [draft, setDraft] = useState<ProviderDraft>(emptyDraft())
  const [editingId, setEditingId] = useState<string | null>(null)
  const [showAdd, setShowAdd] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  async function reloadProviders() {
    const next = await listAIProviders()
    setProviders(next)
    return next
  }

  useEffect(() => {
    let active = true

    async function load() {
      try {
        const [settings, nextProviders] = await Promise.all([getAppSettings(), listAIProviders()])
        if (!active) return
        setForm(formFromSettings(settings))
        setProviders(nextProviders)
        setError('')
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load settings')
        }
      } finally {
        if (active) setLoading(false)
      }
    }

    void load()
    return () => {
      active = false
    }
  }, [])

  function updateField<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((current) => (current ? { ...current, [key]: value } : current))
    setSuccess('')
  }

  async function onSaveProvider(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setError('')
      setSuccess('')
      if (editingId) {
        await updateAIProvider(editingId, {
          sdk: draft.sdk,
          alias: draft.alias.trim(),
          base_url: draft.base_url.trim(),
          ...(draft.api_key.trim() ? { api_key: draft.api_key.trim() } : {}),
        })
      } else {
        await createAIProvider({
          sdk: draft.sdk,
          alias: draft.alias.trim() || sdkLabel(draft.sdk),
          base_url: draft.base_url.trim(),
          api_key: draft.api_key.trim(),
        })
      }
      await reloadProviders()
      setDraft(emptyDraft())
      setEditingId(null)
      setShowAdd(false)
      setSuccess('Provider saved.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save provider')
    }
  }

  async function onDeleteProvider(id: string) {
    try {
      setError('')
      await deleteAIProvider(id)
      await reloadProviders()
      setSuccess('Provider deleted.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete provider')
    }
  }

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return

    const ocrTimeout = Number(form.ocr_timeout_sec)
    const openAITimeout = Number(form.openai_timeout_sec)
    const workerTimeout = Number(form.worker_timeout_sec)
    const maxRetries = Number(form.worker_max_retries)
    const nearThreshold = Number(form.near_duplicate_threshold)

    if (!Number.isFinite(ocrTimeout) || ocrTimeout <= 0) {
      setError('OCR timeout must be a positive number')
      return
    }
    if (!Number.isFinite(openAITimeout) || openAITimeout <= 0) {
      setError('AI timeout must be a positive number')
      return
    }
    if (!Number.isFinite(workerTimeout) || workerTimeout <= 0) {
      setError('Worker timeout must be a positive number')
      return
    }
    if (!Number.isFinite(maxRetries) || maxRetries < 0) {
      setError('Worker max retries must be >= 0')
      return
    }
    if (!Number.isFinite(nearThreshold) || nearThreshold <= 0 || nearThreshold > 1) {
      setError('Near-duplicate threshold must be between 0 and 1')
      return
    }

    try {
      setSaving(true)
      setError('')
      setSuccess('')

      const settings = await updateAppSettings({
        ocr_provider_id: form.ocr_provider_id,
        ocr_model: form.ocr_model,
        extract_provider_id: form.extract_provider_id,
        extract_model: form.extract_model,
        chat_provider_id: form.chat_provider_id,
        chat_model: form.chat_model,
        search_provider_id: form.search_provider_id,
        search_model: form.search_model,
        ocr_timeout_sec: ocrTimeout,
        processing_result_language: form.processing_result_language,
        deep_search_languages: form.deep_search_languages,
        openai_timeout_sec: openAITimeout,
        worker_timeout_sec: workerTimeout,
        worker_max_retries: maxRetries,
        extraction_prompt_version: form.extraction_prompt_version,
        near_duplicate_detection_enabled: form.near_duplicate_detection_enabled,
        near_duplicate_threshold: nearThreshold,
      })

      setForm(formFromSettings(settings))
      setSuccess('Settings saved. Runtime reloaded.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings')
    } finally {
      setSaving(false)
    }
  }

  if (loading || !form) {
    return <p className="text-sm text-stone-500">{error || 'Loading settings...'}</p>
  }

  const llmProviders = providers.filter((item) => isLLMProvider(item.sdk))

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-stone-950">Settings</h1>
        <p className="mt-1 text-sm text-stone-500">
          Runtime configuration for OCR, AI, and the worker. Changes apply immediately.
        </p>
      </div>

      <section className={`${sectionClassName} mb-5`}>
        <h2 className={sectionTitleClassName}>Providers</h2>
        <ul className="mb-4 flex flex-col gap-2">
          {providers.length === 0 && <li className="text-sm text-stone-500">No providers yet.</li>}
          {providers.map((item) => (
            <li
              key={item.id}
              className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-stone-200 bg-white px-3 py-2"
            >
              <div>
                <p className="text-sm font-medium text-stone-950">{item.alias}</p>
                <p className="text-xs text-stone-500">
                  {sdkLabel(item.sdk)}
                  {item.base_url ? ` · ${item.base_url}` : ''}
                  {item.api_key_set ? ' · key set' : ' · missing key'}
                </p>
              </div>
              <div className="flex gap-2">
                <Button
                  variant="secondary"
                  size="xs"
                  onClick={() => {
                    setEditingId(item.id)
                    setShowAdd(true)
                    setDraft({
                      sdk: item.sdk,
                      alias: item.alias,
                      base_url: item.base_url,
                      api_key: '',
                    })
                  }}
                >
                  Edit
                </Button>
                <Button
                  variant="secondary"
                  size="xs"
                  className="text-red-700 hover:bg-red-50"
                  onClick={() => void onDeleteProvider(item.id)}
                >
                  Delete
                </Button>
              </div>
            </li>
          ))}
        </ul>
        {!showAdd ? (
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setEditingId(null)
              setDraft(emptyDraft())
              setShowAdd(true)
            }}
          >
            Add provider
          </Button>
        ) : (
          <form className="grid gap-3 sm:grid-cols-2" onSubmit={onSaveProvider}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>SDK</span>
              <select
                className={inputClassName}
                value={draft.sdk}
                onChange={(event) => {
                  const sdk = event.target.value as ProviderSDK
                  setDraft((current) => ({
                    ...current,
                    sdk,
                    base_url:
                      current.base_url === SDK_DEFAULT_BASE[current.sdk]
                        ? SDK_DEFAULT_BASE[sdk]
                        : current.base_url,
                  }))
                }}
              >
                {SDK_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Alias</span>
              <input
                className={inputClassName}
                value={draft.alias}
                placeholder={sdkLabel(draft.sdk)}
                onChange={(event) => setDraft((current) => ({ ...current, alias: event.target.value }))}
              />
            </label>
            {draft.sdk !== 'google_vision' && (
              <label className={`${labelClassName} sm:col-span-2`}>
                <span className={labelTextClassName}>Base URL</span>
                <input
                  className={inputClassName}
                  value={draft.base_url}
                  onChange={(event) =>
                    setDraft((current) => ({ ...current, base_url: event.target.value }))
                  }
                />
              </label>
            )}
            <label className={`${labelClassName} sm:col-span-2`}>
              <span className={labelTextClassName}>
                API key{editingId ? ' (leave blank to keep)' : ''}
              </span>
              <input
                type="password"
                autoComplete="off"
                className={inputClassName}
                value={draft.api_key}
                required={!editingId}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, api_key: event.target.value }))
                }
              />
            </label>
            <div className="flex gap-2 sm:col-span-2">
              <Button type="submit" size="sm">
                {editingId ? 'Update provider' : 'Save provider'}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  setShowAdd(false)
                  setEditingId(null)
                }}
              >
                Cancel
              </Button>
            </div>
          </form>
        )}
      </section>

      <form className="flex flex-col gap-5" onSubmit={onSubmit}>
        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Models</h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <ProviderModelFields
              label="OCR"
              providers={providers}
              providerId={form.ocr_provider_id}
              model={form.ocr_model}
              purpose="ocr"
              onProviderChange={(id) => updateField('ocr_provider_id', id)}
              onModelChange={(value) => updateField('ocr_model', value)}
            />
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
            <ProviderModelFields
              label="Extraction"
              providers={llmProviders}
              providerId={form.extract_provider_id}
              model={form.extract_model}
              purpose="llm"
              onProviderChange={(id) => updateField('extract_provider_id', id)}
              onModelChange={(value) => updateField('extract_model', value)}
            />
            <ProviderModelFields
              label="Chat"
              providers={llmProviders}
              providerId={form.chat_provider_id}
              model={form.chat_model}
              purpose="llm"
              allowEmpty
              onProviderChange={(id) => updateField('chat_provider_id', id)}
              onModelChange={(value) => updateField('chat_model', value)}
            />
            <ProviderModelFields
              label="Search"
              providers={llmProviders}
              providerId={form.search_provider_id}
              model={form.search_model}
              purpose="llm"
              allowEmpty
              onProviderChange={(id) => updateField('search_provider_id', id)}
              onModelChange={(value) => updateField('search_model', value)}
            />
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
            <label className={labelClassName}>
              <span className={labelTextClassName}>Result language (ISO 639-1)</span>
              <input
                className={inputClassName}
                placeholder="e.g. en"
                value={form.processing_result_language}
                onChange={(e) => updateField('processing_result_language', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Deep search languages</span>
              <input
                className={inputClassName}
                placeholder="e.g. de,en,uk"
                value={form.deep_search_languages}
                onChange={(e) => updateField('deep_search_languages', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Extraction prompt version</span>
              <input
                className={inputClassName}
                value={form.extraction_prompt_version}
                onChange={(e) => updateField('extraction_prompt_version', e.target.value)}
              />
            </label>
          </div>
        </section>

        <SaveSettingsButton saving={saving} />

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Worker</h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <label className={labelClassName}>
              <span className={labelTextClassName}>Job timeout (seconds)</span>
              <input
                type="number"
                min={1}
                className={inputClassName}
                value={form.worker_timeout_sec}
                onChange={(e) => updateField('worker_timeout_sec', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Max retries</span>
              <input
                type="number"
                min={0}
                className={inputClassName}
                value={form.worker_max_retries}
                onChange={(e) => updateField('worker_max_retries', e.target.value)}
              />
            </label>
          </div>
          <p className="mt-3 text-xs text-stone-500">
            Worker cron schedule stays in <code className="font-mono">WORKER_CRON_EXPR</code> in{' '}
            <code className="font-mono">.env</code>.
          </p>
        </section>

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Duplicates</h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <label className="flex items-center gap-2 text-sm text-stone-700 sm:col-span-2">
              <input
                type="checkbox"
                checked={form.near_duplicate_detection_enabled}
                onChange={(e) => updateField('near_duplicate_detection_enabled', e.target.checked)}
              />
              Enable near-duplicate detection after OCR (re-scans)
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Near-duplicate threshold (0–1)</span>
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
          <p className="mt-3 text-xs text-stone-500">
            Exact file duplicates (same checksum) are always rejected on upload. Near-duplicate
            matching compares OCR text and is off by default. Scan existing documents from{' '}
            <Link to="/management" className="underline hover:text-stone-950">
              Management
            </Link>
            .
          </p>
        </section>

        {error && <p className="text-sm text-red-600">{error}</p>}
        {success && <p className="text-sm text-green-700">{success}</p>}

        <SaveSettingsButton saving={saving} />
      </form>
    </div>
  )
}
