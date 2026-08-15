import { type SubmitEvent, useEffect, useState } from 'react'
import {
  accentContrastText,
  createAIProvider,
  createSetupAdmin,
  getAppSettings,
  getSetupStatus,
  listAIProviders,
  loginWithPassword,
  updateAppSettings,
  type AIProvider,
  type ProviderSDK,
  type SetupStatus,
} from '../lib/pocketbase'
import { AppFooter } from './AppFooter'
import { ProviderModelFields, isLLMProvider, sdkLabel } from './ProviderModelFields'

type SetupWizardProps = {
  appName: string
  accent: string
  initialStatus: SetupStatus
  onComplete: () => void
}

type Step = 'admin' | 'providers' | 'models' | 'done'

const inputClassName =
  'w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900'
const labelClassName = 'flex flex-col gap-1'
const labelTextClassName = 'text-xs font-medium text-stone-500'

const SDK_DEFAULT_BASE: Record<ProviderSDK, string> = {
  openai: 'https://api.openai.com/v1',
  openrouter: 'https://openrouter.ai/api/v1',
  mistral: 'https://api.mistral.ai/v1',
  google_vision: '',
}

function initialStep(status: SetupStatus): Step {
  if (status.needs_admin) return 'admin'
  if (status.needs_config) {
    if (!status.provider_count) return 'providers'
    return 'models'
  }
  return 'done'
}

export function SetupWizard({ appName, accent, initialStatus, onComplete }: SetupWizardProps) {
  const appInitial = appName.trim().charAt(0).toUpperCase() || 'P'
  const logoStyle = { backgroundColor: accent, color: accentContrastText(accent) }

  const [step, setStep] = useState<Step>(() => initialStep(initialStatus))
  const [status, setStatus] = useState(initialStatus)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [passwordConfirm, setPasswordConfirm] = useState('')

  const [providers, setProviders] = useState<AIProvider[]>([])
  const [sdk, setSdk] = useState<ProviderSDK>('openai')
  const [alias, setAlias] = useState('')
  const [baseURL, setBaseURL] = useState(SDK_DEFAULT_BASE.openai)
  const [apiKey, setApiKey] = useState('')

  const [ocrProviderId, setOcrProviderId] = useState('')
  const [ocrModel, setOcrModel] = useState('')
  const [extractProviderId, setExtractProviderId] = useState('')
  const [extractModel, setExtractModel] = useState('')

  useEffect(() => {
    if (step === 'admin') return

    let active = true
    async function load() {
      try {
        const [nextProviders, settings] = await Promise.all([listAIProviders(), getAppSettings()])
        if (!active) return
        setProviders(nextProviders)
        setOcrProviderId(settings.ocr_provider_id || nextProviders[0]?.id || '')
        setOcrModel(settings.ocr_model || '')
        const llm = nextProviders.find((item) => isLLMProvider(item.sdk))
        setExtractProviderId(settings.extract_provider_id || llm?.id || '')
        setExtractModel(settings.extract_model || '')
      } catch {
        // Prefill is best-effort.
      }
    }
    void load()
    return () => {
      active = false
    }
  }, [step])

  async function refreshStatus() {
    const next = await getSetupStatus()
    setStatus(next)
    return next
  }

  async function onCreateAdmin(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setSubmitting(true)
      setError('')
      if (password !== passwordConfirm) {
        throw new Error('Passwords do not match.')
      }
      await createSetupAdmin(email.trim(), password, passwordConfirm)
      await loginWithPassword(email.trim(), password)
      const next = await refreshStatus()
      if (!next.needs_config) {
        setStep('done')
        return
      }
      setStep(next.provider_count ? 'models' : 'providers')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create admin')
    } finally {
      setSubmitting(false)
    }
  }

  async function onSaveProvider(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setSubmitting(true)
      setError('')
      if (!apiKey.trim()) {
        throw new Error('Enter an API key.')
      }
      await createAIProvider({
        sdk,
        alias: alias.trim() || sdkLabel(sdk),
        base_url: baseURL.trim(),
        api_key: apiKey.trim(),
      })
      const nextProviders = await listAIProviders()
      setProviders(nextProviders)
      setApiKey('')
      setAlias('')
      const next = await refreshStatus()
      if (!next.needs_config) {
        setStep('done')
        return
      }
      setStep('models')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save provider')
    } finally {
      setSubmitting(false)
    }
  }

  async function onSaveModels(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setSubmitting(true)
      setError('')
      if (!ocrProviderId) {
        throw new Error('Choose an OCR provider.')
      }
      if (!extractProviderId) {
        throw new Error('Choose an extraction provider (OpenAI, OpenRouter, or Mistral).')
      }
      await updateAppSettings({
        ocr_provider_id: ocrProviderId,
        ocr_model: ocrModel,
        extract_provider_id: extractProviderId,
        extract_model: extractModel,
      })
      const next = await refreshStatus()
      if (next.needs_config) {
        if (!next.has_ocr || !next.has_llm) {
          setStep(next.provider_count ? 'models' : 'providers')
        }
        setError('Setup is still incomplete. Add an OCR provider and an LLM provider (OpenAI, OpenRouter, or Mistral).')
        return
      }
      setStep('done')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save models')
    } finally {
      setSubmitting(false)
    }
  }

  const stepLabel =
    step === 'admin'
      ? '1 · Admin account'
      : step === 'providers'
        ? '2 · Provider'
        : step === 'models'
          ? '3 · Models'
          : 'Ready'

  const llmProviders = providers.filter((item) => isLLMProvider(item.sdk))

  return (
    <div className="flex min-h-screen flex-col bg-stone-100">
      <div className="flex flex-1 items-center justify-center px-6 py-10">
        <section className="w-full max-w-md rounded-lg border border-stone-200 bg-stone-50 p-6 shadow-sm">
          <div className="mb-2 flex items-center gap-2">
            <span
              className="flex h-7 w-7 items-center justify-center rounded-md text-sm"
              style={logoStyle}
            >
              {appInitial}
            </span>
            <h1 className="text-lg font-semibold text-stone-950">{appName}</h1>
          </div>
          <p className="mb-1 text-xs font-medium uppercase tracking-wide text-stone-400">{stepLabel}</p>
          <h2 className="mb-4 text-base font-semibold text-stone-900">
            {step === 'admin' && 'Create your admin account'}
            {step === 'providers' && 'Add a provider'}
            {step === 'models' && 'Choose models'}
            {step === 'done' && 'Setup complete'}
          </h2>

          {step === 'admin' && (
            <form className="flex flex-col gap-4" onSubmit={onCreateAdmin}>
              <p className="text-sm text-stone-600">
                This account manages settings and can access PocketBase Admin.
              </p>
              <label className={labelClassName}>
                <span className={labelTextClassName}>Email</span>
                <input
                  type="email"
                  autoComplete="email"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  className={inputClassName}
                />
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>Password</span>
                <input
                  type="password"
                  autoComplete="new-password"
                  required
                  minLength={8}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  className={inputClassName}
                />
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>Confirm password</span>
                <input
                  type="password"
                  autoComplete="new-password"
                  required
                  minLength={8}
                  value={passwordConfirm}
                  onChange={(e) => setPasswordConfirm(e.target.value)}
                  className={inputClassName}
                />
              </label>
              {error && <p className="text-sm text-red-600">{error}</p>}
              <button
                type="submit"
                disabled={submitting}
                className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {submitting ? 'Creating...' : 'Create admin'}
              </button>
            </form>
          )}

          {step === 'providers' && (
            <form className="flex flex-col gap-4" onSubmit={onSaveProvider}>
              <p className="text-sm text-stone-600">
                Add an API provider. OpenAI, OpenRouter, or Mistral can run extraction and chat;
                Google Vision or Mistral OCR can run OCR. One Mistral provider covers both.
              </p>
              {providers.length > 0 && (
                <p className="text-xs text-stone-500">
                  Already added: {providers.map((item) => item.alias).join(', ')}
                </p>
              )}
              <label className={labelClassName}>
                <span className={labelTextClassName}>SDK</span>
                <select
                  value={sdk}
                  onChange={(e) => {
                    const next = e.target.value as ProviderSDK
                    setSdk(next)
                    setBaseURL(SDK_DEFAULT_BASE[next])
                  }}
                  className={inputClassName}
                >
                  <option value="openai">OpenAI</option>
                  <option value="openrouter">OpenRouter</option>
                  <option value="mistral">Mistral</option>
                  <option value="google_vision">Google Cloud Vision</option>
                </select>
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>Alias</span>
                <input
                  value={alias}
                  placeholder={sdkLabel(sdk)}
                  onChange={(e) => setAlias(e.target.value)}
                  className={inputClassName}
                />
              </label>
              {sdk !== 'google_vision' && (
                <label className={labelClassName}>
                  <span className={labelTextClassName}>Base URL</span>
                  <input
                    type="url"
                    value={baseURL}
                    onChange={(e) => setBaseURL(e.target.value)}
                    className={inputClassName}
                  />
                </label>
              )}
              <label className={labelClassName}>
                <span className={labelTextClassName}>API key</span>
                <input
                  type="password"
                  autoComplete="off"
                  required
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  className={inputClassName}
                />
              </label>
              {error && <p className="text-sm text-red-600">{error}</p>}
              <button
                type="submit"
                disabled={submitting}
                className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {submitting ? 'Saving...' : 'Continue'}
              </button>
            </form>
          )}

          {step === 'models' && (
            <form className="flex flex-col gap-4" onSubmit={onSaveModels}>
              <p className="text-sm text-stone-600">
                Pick a provider and model for OCR and metadata extraction. Chat and search default to
                the extraction model.
              </p>
              {llmProviders.length === 0 && (
                <p className="text-sm text-amber-800">
                  Add an OpenAI, OpenRouter, or Mistral provider to enable extraction and chat.
                </p>
              )}
              <ProviderModelFields
                label="OCR"
                providers={providers}
                providerId={ocrProviderId}
                model={ocrModel}
                purpose="ocr"
                onProviderChange={setOcrProviderId}
                onModelChange={setOcrModel}
              />
              <ProviderModelFields
                label="Extraction"
                providers={llmProviders}
                providerId={extractProviderId}
                model={extractModel}
                purpose="llm"
                onProviderChange={setExtractProviderId}
                onModelChange={setExtractModel}
              />
              {error && <p className="text-sm text-red-600">{error}</p>}
              <div className="flex flex-col gap-2">
                <button
                  type="submit"
                  disabled={submitting}
                  className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {submitting ? 'Saving...' : 'Finish setup'}
                </button>
                <button
                  type="button"
                  className="text-left text-xs font-medium text-stone-500 hover:text-stone-800"
                  onClick={() => setStep('providers')}
                >
                  Add another provider
                </button>
              </div>
            </form>
          )}

          {step === 'done' && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-stone-600">
                Your admin account and processing keys are ready. You can change them anytime in
                Settings.
              </p>
              {status.needs_config && (
                <p className="text-sm text-red-600">Setup still reports missing configuration.</p>
              )}
              <button
                type="button"
                onClick={onComplete}
                className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700"
              >
                Open {appName}
              </button>
            </div>
          )}
        </section>
      </div>
      <AppFooter />
    </div>
  )
}

type SetupBlockedProps = {
  appName: string
  accent: string
  onLogout: () => void
}

export function SetupBlocked({ appName, accent, onLogout }: SetupBlockedProps) {
  const appInitial = appName.trim().charAt(0).toUpperCase() || 'P'
  const logoStyle = { backgroundColor: accent, color: accentContrastText(accent) }

  return (
    <div className="flex min-h-screen flex-col bg-stone-100">
      <div className="flex flex-1 items-center justify-center px-6">
        <section className="w-full max-w-sm rounded-lg border border-stone-200 bg-stone-50 p-6 shadow-sm">
          <div className="mb-4 flex items-center gap-2">
            <span
              className="flex h-7 w-7 items-center justify-center rounded-md text-sm"
              style={logoStyle}
            >
              {appInitial}
            </span>
            <h1 className="text-lg font-semibold text-stone-950">{appName}</h1>
          </div>
          <h2 className="mb-2 text-base font-semibold text-stone-900">Setup incomplete</h2>
          <p className="mb-4 text-sm text-stone-600">
            An administrator must finish first-launch configuration before the app can be used.
          </p>
          <button
            type="button"
            onClick={onLogout}
            className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700"
          >
            Log out
          </button>
        </section>
      </div>
      <AppFooter />
    </div>
  )
}
