import { type SubmitEvent, useEffect, useState } from 'react'
import { Navigate } from '@tanstack/react-router'
import {
  ensureAuth,
  getAppSettings,
  isSuperuser,
  scanDuplicates,
  updateAppSettings,
  type AppSettings,
  type DuplicateScanResult,
} from '../lib/pocketbase'

const inputClassName =
  'w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900'
const labelClassName = 'flex flex-col gap-1'
const labelTextClassName = 'text-xs font-medium text-stone-500'
const sectionClassName = 'rounded-lg border border-stone-200 bg-stone-50 p-5'
const sectionTitleClassName = 'mb-4 text-sm font-semibold text-stone-950'

type FormState = {
  ocr_provider: string
  google_vision_api_key: string
  mistral_api_key: string
  mistral_ocr_model: string
  mistral_api_base_url: string
  ocr_timeout_sec: string
  processing_result_language: string
  deep_search_languages: string
  openai_api_key: string
  openai_model: string
  openai_chat_model: string
  openai_search_model: string
  openai_base_url: string
  openai_timeout_sec: string
  worker_timeout_sec: string
  worker_max_retries: string
  extraction_prompt_version: string
  near_duplicate_detection_enabled: boolean
  near_duplicate_threshold: string
}

function formFromSettings(settings: AppSettings): FormState {
  return {
    ocr_provider: settings.ocr_provider,
    google_vision_api_key: '',
    mistral_api_key: '',
    mistral_ocr_model: settings.mistral_ocr_model,
    mistral_api_base_url: settings.mistral_api_base_url,
    ocr_timeout_sec: String(settings.ocr_timeout_sec),
    processing_result_language: settings.processing_result_language,
    deep_search_languages: settings.deep_search_languages,
    openai_api_key: '',
    openai_model: settings.openai_model,
    openai_chat_model: settings.openai_chat_model,
    openai_search_model: settings.openai_search_model,
    openai_base_url: settings.openai_base_url,
    openai_timeout_sec: String(settings.openai_timeout_sec),
    worker_timeout_sec: String(settings.worker_timeout_sec),
    worker_max_retries: String(settings.worker_max_retries),
    extraction_prompt_version: settings.extraction_prompt_version,
    near_duplicate_detection_enabled: settings.near_duplicate_detection_enabled,
    near_duplicate_threshold: String(settings.near_duplicate_threshold ?? 0.92),
  }
}

export function SettingsPage() {
  const [allowed, setAllowed] = useState<boolean | null>(null)
  const [form, setForm] = useState<FormState | null>(null)
  const [keyFlags, setKeyFlags] = useState({
    google: false,
    mistral: false,
    openai: false,
  })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [scanning, setScanning] = useState(false)
  const [scanResult, setScanResult] = useState<DuplicateScanResult | null>(null)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  useEffect(() => {
    let active = true

    async function load() {
      try {
        await ensureAuth()
        if (!isSuperuser()) {
          if (active) setAllowed(false)
          return
        }
        if (active) setAllowed(true)

        const settings = await getAppSettings()
        if (!active) return
        setForm(formFromSettings(settings))
        setKeyFlags({
          google: settings.google_vision_api_key_set,
          mistral: settings.mistral_api_key_set,
          openai: settings.openai_api_key_set,
        })
        setError('')
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load settings')
          setAllowed(isSuperuser())
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
      setError('OpenAI timeout must be a positive number')
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
        ocr_provider: form.ocr_provider,
        mistral_ocr_model: form.mistral_ocr_model,
        mistral_api_base_url: form.mistral_api_base_url,
        ocr_timeout_sec: ocrTimeout,
        processing_result_language: form.processing_result_language,
        deep_search_languages: form.deep_search_languages,
        openai_model: form.openai_model,
        openai_chat_model: form.openai_chat_model,
        openai_search_model: form.openai_search_model,
        openai_base_url: form.openai_base_url,
        openai_timeout_sec: openAITimeout,
        worker_timeout_sec: workerTimeout,
        worker_max_retries: maxRetries,
        extraction_prompt_version: form.extraction_prompt_version,
        near_duplicate_detection_enabled: form.near_duplicate_detection_enabled,
        near_duplicate_threshold: nearThreshold,
        ...(form.google_vision_api_key.trim()
          ? { google_vision_api_key: form.google_vision_api_key.trim() }
          : {}),
        ...(form.mistral_api_key.trim() ? { mistral_api_key: form.mistral_api_key.trim() } : {}),
        ...(form.openai_api_key.trim() ? { openai_api_key: form.openai_api_key.trim() } : {}),
      })

      setForm(formFromSettings(settings))
      setKeyFlags({
        google: settings.google_vision_api_key_set,
        mistral: settings.mistral_api_key_set,
        openai: settings.openai_api_key_set,
      })
      setSuccess('Settings saved. Runtime reloaded.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings')
    } finally {
      setSaving(false)
    }
  }

  async function onScanDuplicates() {
    try {
      setScanning(true)
      setError('')
      setScanResult(null)
      const result = await scanDuplicates()
      setScanResult(result)
      setSuccess(
        `Scan finished: ${result.scanned} scanned, ${result.exact_marked} exact marked, ${result.near_marked} near marked.`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Duplicate scan failed')
    } finally {
      setScanning(false)
    }
  }

  if (allowed === false) {
    return <Navigate to="/" />
  }

  if (loading || !form) {
    return <p className="text-sm text-stone-500">{error || 'Loading settings...'}</p>
  }

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-stone-950">Settings</h1>
        <p className="mt-1 text-sm text-stone-500">
          Runtime configuration for OCR, AI, and the worker. Changes apply immediately.
        </p>
      </div>

      <form className="flex flex-col gap-5" onSubmit={onSubmit}>
        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>OCR</h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <label className={labelClassName}>
              <span className={labelTextClassName}>Provider</span>
              <select
                className={inputClassName}
                value={form.ocr_provider}
                onChange={(e) => updateField('ocr_provider', e.target.value)}
              >
                <option value="google_vision">Google Cloud Vision</option>
                <option value="mistral">Mistral OCR</option>
              </select>
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Timeout (seconds)</span>
              <input
                type="number"
                min={1}
                className={inputClassName}
                value={form.ocr_timeout_sec}
                onChange={(e) => updateField('ocr_timeout_sec', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>
                Google Vision API key{keyFlags.google ? ' (set)' : ''}
              </span>
              <input
                type="password"
                autoComplete="off"
                placeholder={keyFlags.google ? '•••• leave blank to keep' : ''}
                className={inputClassName}
                value={form.google_vision_api_key}
                onChange={(e) => updateField('google_vision_api_key', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>
                Mistral API key{keyFlags.mistral ? ' (set)' : ''}
              </span>
              <input
                type="password"
                autoComplete="off"
                placeholder={keyFlags.mistral ? '•••• leave blank to keep' : ''}
                className={inputClassName}
                value={form.mistral_api_key}
                onChange={(e) => updateField('mistral_api_key', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Mistral OCR model</span>
              <input
                className={inputClassName}
                value={form.mistral_ocr_model}
                onChange={(e) => updateField('mistral_ocr_model', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Mistral API base URL</span>
              <input
                className={inputClassName}
                value={form.mistral_api_base_url}
                onChange={(e) => updateField('mistral_api_base_url', e.target.value)}
              />
            </label>
          </div>
        </section>

        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>AI / OpenAI</h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <label className={`${labelClassName} sm:col-span-2`}>
              <span className={labelTextClassName}>
                API key{keyFlags.openai ? ' (set)' : ''}
              </span>
              <input
                type="password"
                autoComplete="off"
                placeholder={keyFlags.openai ? '•••• leave blank to keep' : ''}
                className={inputClassName}
                value={form.openai_api_key}
                onChange={(e) => updateField('openai_api_key', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Extraction model</span>
              <input
                className={inputClassName}
                value={form.openai_model}
                onChange={(e) => updateField('openai_model', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Chat model</span>
              <input
                className={inputClassName}
                value={form.openai_chat_model}
                onChange={(e) => updateField('openai_chat_model', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Search model</span>
              <input
                className={inputClassName}
                placeholder="Defaults to chat model"
                value={form.openai_search_model}
                onChange={(e) => updateField('openai_search_model', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Base URL</span>
              <input
                className={inputClassName}
                value={form.openai_base_url}
                onChange={(e) => updateField('openai_base_url', e.target.value)}
              />
            </label>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Timeout (seconds)</span>
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
            matching compares OCR text and is off by default.
          </p>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <button
              type="button"
              disabled={scanning}
              onClick={() => void onScanDuplicates()}
              className="rounded-md border border-stone-300 bg-white px-4 py-2 text-sm font-medium text-stone-950 transition-colors hover:bg-stone-100 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {scanning ? 'Scanning...' : 'Scan for duplicates'}
            </button>
            {scanResult && (
              <p className="text-xs text-stone-500">
                Backfilled {scanResult.checksum_backfilled} checksums,{' '}
                {scanResult.fingerprints_filled} fingerprints.
              </p>
            )}
          </div>
        </section>

        {error && <p className="text-sm text-red-600">{error}</p>}
        {success && <p className="text-sm text-green-700">{success}</p>}

        <div>
          <button
            type="submit"
            disabled={saving}
            className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {saving ? 'Saving...' : 'Save settings'}
          </button>
        </div>
      </form>
    </div>
  )
}
