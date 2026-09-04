import { type SubmitEvent, useEffect, useState } from 'react'
import { loginWithPassword } from '../lib/auth'
import { registerPasskey } from '../lib/api/passkeys'
import { defaultPasskeyName, passkeysSupported } from '../lib/webauthn'
import { createSetupAdmin, getSetupStatus, type SetupStatus } from '../lib/api/meta'
import {
  createAIProvider,
  isLLMProvider,
  listAIProviders,
  requiresAPIKey,
  sdkLabel,
  LOCAL_OCR_HINT,
  SDK_DEFAULT_BASE,
  SDK_OPTIONS,
  type AIProvider,
  type ProviderSDK,
} from '../lib/api/providers'
import { getAppSettings, updateAppSettings } from '../lib/api/settings'
import { AppFooter } from './AppFooter'
import { ProviderModelFields } from './ProviderModelFields'
import {
  AppLogo,
  Button,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
} from './ui'

type SetupWizardProps = {
  appName: string
  accent: string
  initialStatus: SetupStatus
  onComplete: () => void
}

type Step = 'admin' | 'passkey' | 'providers' | 'models' | 'done'

// Deliberately never returns 'passkey'. SetupStatus has no notion of passkeys, so
// the optional step is reachable only from the in-session transition out of
// 'admin' -- which is the whole mechanism that stops it resurfacing on a later
// boot, or on a wizard that resumes at 'providers'.
function initialStep(status: SetupStatus): Step {
  if (status.needs_admin) return 'admin'
  return nextConfigStep(status)
}

// The step to land on once the admin account exists.
function nextConfigStep(status: SetupStatus): Step {
  if (!status.needs_config) return 'done'
  return status.provider_count ? 'models' : 'providers'
}

export function SetupWizard({ appName, accent, initialStatus, onComplete }: SetupWizardProps) {
  const [step, setStep] = useState<Step>(() => initialStep(initialStatus))
  const [status, setStatus] = useState(initialStatus)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [passwordConfirm, setPasswordConfirm] = useState('')

  // Where to go once the optional passkey step is done or skipped. Stashed rather
  // than re-derived so both paths land in the same place without a second round
  // trip; nothing can change the setup status while the dialog is open.
  const [afterPasskey, setAfterPasskey] = useState<Step>('done')
  const [passkeyName, setPasskeyName] = useState('')

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
      const target = nextConfigStep(next)
      setAfterPasskey(target)
      // Offered only where it can actually work. An install reached over plain
      // HTTP on a LAN address cannot create a passkey, and a dead end here would
      // be worse than not asking.
      if (passkeysSupported()) {
        setPasskeyName(defaultPasskeyName())
        setStep('passkey')
        return
      }
      setStep(target)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create admin')
    } finally {
      setSubmitting(false)
    }
  }

  async function onAddPasskey(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setSubmitting(true)
      setError('')
      await registerPasskey(passkeyName.trim() || defaultPasskeyName())
      setStep(afterPasskey)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add the passkey')
    } finally {
      setSubmitting(false)
    }
  }

  async function onSaveProvider(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setSubmitting(true)
      setError('')
      if (requiresAPIKey(sdk) && !apiKey.trim()) {
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
      // First-launch setup only asks for one LLM binding: chat and search start
      // out pointing at the extraction provider/model and can be split later in
      // Settings.
      await updateAppSettings({
        ocr_provider_id: ocrProviderId,
        ocr_model: ocrModel,
        extract_provider_id: extractProviderId,
        extract_model: extractModel,
        chat_provider_id: extractProviderId,
        chat_model: extractModel,
        search_provider_id: extractProviderId,
        search_model: extractModel,
      })
      const next = await refreshStatus()
      if (next.needs_config) {
        if (!next.has_ocr || !next.has_llm) {
          setStep(next.provider_count ? 'models' : 'providers')
        }
        setError(
          'Setup is still incomplete. Add an OCR provider and an LLM provider (OpenAI, OpenRouter, or Mistral).',
        )
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
      : step === 'passkey'
        ? 'Optional · Passkey'
        : step === 'providers'
          ? '2 · Provider'
          : step === 'models'
            ? '3 · Models'
            : 'Ready'

  const llmProviders = providers.filter((item) => isLLMProvider(item.sdk))

  return (
    <div className="flex min-h-screen flex-col bg-paper">
      <div className="flex flex-1 items-center justify-center px-6 py-10">
        <section className="w-full max-w-md border border-line-strong bg-surface p-8 shadow-sm shadow-ink/10">
          <div className="mb-2 flex items-center gap-2">
            <AppLogo appName={appName} accent={accent} />
            <h1 className="font-display text-xl font-semibold text-ink">{appName}</h1>
          </div>
          <p className="mb-1 text-[10px] font-semibold uppercase tracking-[0.2em] text-oxblood">
            {stepLabel}
          </p>
          <h2 className="mb-4 font-display text-lg font-semibold text-ink">
            {step === 'admin' && 'Create your admin account'}
            {step === 'passkey' && 'Add a passkey'}
            {step === 'providers' && 'Add a provider'}
            {step === 'models' && 'Choose models'}
            {step === 'done' && 'Setup complete'}
          </h2>

          {step === 'admin' && (
            <form className="flex flex-col gap-4" onSubmit={onCreateAdmin}>
              <p className="text-sm text-ink-muted">
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
              {error && <p className="text-sm text-madder">{error}</p>}
              <Button type="submit" disabled={submitting}>
                {submitting ? 'Creating...' : 'Create admin'}
              </Button>
            </form>
          )}

          {step === 'passkey' && (
            <form className="flex flex-col gap-4" onSubmit={onAddPasskey}>
              <p className="text-sm text-ink-muted">
                Sign in later with your fingerprint, face, or device PIN instead of a password. You
                can add or remove passkeys anytime from the Account page.
              </p>
              <label className={labelClassName}>
                <span className={labelTextClassName}>Name</span>
                <input
                  value={passkeyName}
                  onChange={(e) => setPasskeyName(e.target.value)}
                  className={inputClassName}
                />
              </label>
              {error && <p className="text-sm text-madder">{error}</p>}
              <Button type="submit" disabled={submitting}>
                {submitting ? 'Waiting for your device...' : 'Create passkey'}
              </Button>
              {/* Never disabled: a failure on this step must not trap anyone in
                  an optional detour. */}
              <button
                type="button"
                className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                onClick={() => setStep(afterPasskey)}
              >
                Skip for now
              </button>
            </form>
          )}

          {step === 'providers' && (
            <form className="flex flex-col gap-4" onSubmit={onSaveProvider}>
              <p className="text-sm text-ink-muted">
                Add a provider. OpenAI, OpenRouter, or Mistral can run extraction and chat;
                Google Vision or Mistral OCR can run OCR, and one Mistral provider covers both.
                Docling runs OCR on your own host — no API key, but you need the local-OCR
                compose overlay running first.
              </p>
              {providers.length > 0 && (
                <p className="text-xs text-ink-soft">
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
              {requiresAPIKey(sdk) ? (
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
              ) : (
                <p className={fieldHintClassName}>{LOCAL_OCR_HINT}</p>
              )}
              {error && <p className="text-sm text-madder">{error}</p>}
              <Button type="submit" disabled={submitting}>
                {submitting ? 'Saving...' : 'Continue'}
              </Button>
            </form>
          )}

          {step === 'models' && (
            <form className="flex flex-col gap-4" onSubmit={onSaveModels}>
              <p className="text-sm text-ink-muted">
                Pick a provider and model for OCR and metadata extraction. Chat and search are set
                to the extraction model too; you can change them later in Settings.
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
              {error && <p className="text-sm text-madder">{error}</p>}
              <div className="flex flex-col gap-2">
                <Button type="submit" disabled={submitting}>
                  {submitting ? 'Saving...' : 'Finish setup'}
                </Button>
                <button
                  type="button"
                  className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                  onClick={() => setStep('providers')}
                >
                  Add another provider
                </button>
              </div>
            </form>
          )}

          {step === 'done' && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-ink-muted">
                Your admin account and processing keys are ready. You can change them anytime in
                Settings.
              </p>
              {status.needs_config && (
                <p className="text-sm text-madder">Setup still reports missing configuration.</p>
              )}
              <Button onClick={onComplete}>Open {appName}</Button>
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
  return (
    <div className="flex min-h-screen flex-col bg-paper">
      <div className="flex flex-1 items-center justify-center px-6">
        <section className="w-full max-w-sm border border-line-strong bg-surface p-8 shadow-sm shadow-ink/10">
          <div className="mb-4 flex items-center gap-2">
            <AppLogo appName={appName} accent={accent} />
            <h1 className="font-display text-xl font-semibold text-ink">{appName}</h1>
          </div>
          <h2 className="mb-2 font-display text-lg font-semibold text-ink">Setup incomplete</h2>
          <p className="mb-4 text-sm text-ink-muted">
            An administrator must finish first-launch configuration before the app can be used.
          </p>
          <Button onClick={onLogout}>Log out</Button>
        </section>
      </div>
      <AppFooter />
    </div>
  )
}
