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
import { useAppMeta } from '../hooks/useAppMeta'
import {
  Button,
  inputClassName,
  labelClassName,
  fieldHintClassName,
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
  search_context_tokens: string
  openai_timeout_sec: string
  worker_timeout_sec: string
  worker_max_retries: string
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
    search_context_tokens: String(settings.search_context_tokens),
    openai_timeout_sec: String(settings.openai_timeout_sec),
    worker_timeout_sec: String(settings.worker_timeout_sec),
    worker_max_retries: String(settings.worker_max_retries),
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
  // On a managed instance the hosting provider owns the providers, the model
  // bindings and duplicate detection: the container's environment rewrites them
  // on every boot, and the API refuses to change them. Rendering the fields
  // anyway would offer an edit that silently disappears at the next restart.
  //
  // Unknown counts as managed. The flag arrives a moment after the first paint,
  // and treating that moment as "not managed" would flash the operator-owned
  // sections into view; treating a failed meta request that way would leave them
  // there for good, and Save would then send fields the server rejects — which
  // fails the whole patch and loses the timeout edit next to them.
  const { aiManaged } = useAppMeta()
  const aiEditable = aiManaged === false
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
    const searchContextTokens = Number(form.search_context_tokens)

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
    if (!Number.isFinite(searchContextTokens) || searchContextTokens <= 0) {
      setError('Search context window must be a positive number of tokens')
      return
    }

    try {
      setSaving(true)
      setError('')
      setSuccess('')

      const settings = await updateAppSettings({
        ocr_timeout_sec: ocrTimeout,
        processing_result_language: form.processing_result_language,
        deep_search_languages: form.deep_search_languages,
        openai_timeout_sec: openAITimeout,
        worker_timeout_sec: workerTimeout,
        worker_max_retries: maxRetries,
        // Omitted rather than sent unchanged on a managed instance: the server
        // refuses the whole patch if it names one of these, so including them
        // would fail the save of the fields that are still editable.
        ...(aiEditable
          ? {
              ocr_provider_id: form.ocr_provider_id,
              ocr_model: form.ocr_model,
              extract_provider_id: form.extract_provider_id,
              extract_model: form.extract_model,
              chat_provider_id: form.chat_provider_id,
              chat_model: form.chat_model,
              search_provider_id: form.search_provider_id,
              search_model: form.search_model,
              search_context_tokens: searchContextTokens,
              near_duplicate_detection_enabled: form.near_duplicate_detection_enabled,
              near_duplicate_threshold: nearThreshold,
            }
          : {}),
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
    return <p className="text-sm text-ink-soft">{error || 'Loading settings...'}</p>
  }

  const llmProviders = providers.filter((item) => isLLMProvider(item.sdk))

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6">
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">Settings</h1>
        <p className="mt-1 text-sm text-ink-soft">
          Runtime configuration for OCR, AI, and the worker. Changes apply immediately.
        </p>
        {!aiEditable && (
          <p className="mt-2 text-sm text-ink-soft">
            AI providers and models are set by your hosting provider and are not editable here.
          </p>
        )}
      </div>

      {aiEditable && (
        <section className={`${sectionClassName} mb-5`}>
          <h2 className={sectionTitleClassName}>Providers</h2>
          <ul className="mb-4 flex flex-col gap-2">
            {providers.length === 0 && <li className="text-sm text-ink-soft">No providers yet.</li>}
            {providers.map((item) => (
              <li
                key={item.id}
                className="flex flex-wrap items-center justify-between gap-2 rounded-xs border border-line bg-bright px-3 py-2"
              >
                <div>
                  <p className="text-sm font-medium text-ink">{item.alias}</p>
                  <p className="text-xs text-ink-soft">
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
                    className="text-madder hover:bg-madder/10"
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
      )}

      <form className="flex flex-col gap-5" onSubmit={onSubmit}>
        {aiEditable && (
          <section className={sectionClassName}>
            <h2 className={sectionTitleClassName}>Models</h2>
            <div className="grid gap-4 sm:grid-cols-2">
              <ProviderModelFields
                label="OCR"
                help="Reads the text out of uploaded PDFs, images and scans. Plain text, CSV, Word and Excel files are read locally and skip this step."
                providers={providers}
                providerId={form.ocr_provider_id}
                model={form.ocr_model}
                purpose="ocr"
                onProviderChange={(id) => updateField('ocr_provider_id', id)}
                onModelChange={(value) => updateField('ocr_model', value)}
              />
              <ProviderModelFields
                label="Extraction"
                help="Turns a document's text into its title, date, type, correspondent, tags and summary. Also proposes the cuts for Detect automatically when splitting a PDF."
                providers={llmProviders}
                providerId={form.extract_provider_id}
                model={form.extract_model}
                purpose="llm"
                onProviderChange={(id) => updateField('extract_provider_id', id)}
                onModelChange={(value) => updateField('extract_model', value)}
              />
              <ProviderModelFields
                label="Chat"
                help="Answers questions about a single document on its Ask AI page. Leave the provider empty to turn the feature off."
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
                help="Answers natural-language queries on the Deep Search page, in both Search and Research mode. Leave the provider empty to turn the feature off."
                providers={llmProviders}
                providerId={form.search_provider_id}
                model={form.search_model}
                purpose="llm"
                allowEmpty
                onProviderChange={(id) => updateField('search_provider_id', id)}
                onModelChange={(value, meta) => {
                  updateField('search_model', value)
                  // Research reads documents until this window is spent, so a
                  // stale window from a previously chosen model is worth
                  // correcting the moment the provider tells us the real one.
                  if (meta?.context_window) {
                    updateField('search_context_tokens', String(meta.context_window))
                  }
                }}
              />
              <div className={labelClassName}>
                <label className={labelClassName}>
                  <span className={labelTextClassName}>Search context window (tokens)</span>
                  <input
                    type="number"
                    min={1}
                    className={inputClassName}
                    value={form.search_context_tokens}
                    onChange={(e) => updateField('search_context_tokens', e.target.value)}
                  />
                </label>
                <p className={fieldHintClassName}>
                  The search model&rsquo;s context window. Research mode keeps searching and reading
                  documents until this budget is spent, so it is the only limit on how much of your
                  archive one question can draw on. Filled in automatically when the provider reports
                  it; OpenAI does not.
                </p>
              </div>
            </div>
          </section>
        )}

        {/*
          Kept out of Models on purpose: none of these is a provider or a model,
          so a managed tenant keeps them. The environment seeds them on the first
          boot and never touches them again, in either mode.
        */}
        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Processing</h2>
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
                Languages deep search translates keywords into, so a German invoice is found by an
                English question. Leave empty to search only in the language of the question.
              </p>
            </div>
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
          <p className="mt-3 text-xs text-ink-soft">
            Worker cron schedule stays in <code className="font-mono">WORKER_CRON_EXPR</code> in{' '}
            <code className="font-mono">.env</code>.
          </p>
        </section>

        {aiEditable && (
          <section className={sectionClassName}>
            <h2 className={sectionTitleClassName}>Duplicates</h2>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="flex items-center gap-2 text-sm text-ink-muted sm:col-span-2">
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
            <p className="mt-3 text-xs text-ink-soft">
              Exact file duplicates (same checksum) are always rejected on upload. Near-duplicate
              matching compares OCR text and is off by default. Scan existing documents from{' '}
              <Link to="/management" className="underline hover:text-oxblood">
                Management
              </Link>
              .
            </p>
          </section>
        )}

        {error && <p className="text-sm text-madder">{error}</p>}
        {success && <p className="text-sm text-forest">{success}</p>}

        <SaveSettingsButton saving={saving} />
      </form>
    </div>
  )
}
