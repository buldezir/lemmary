import { type SubmitEvent, useEffect, useState } from 'react'
import { loginWithPassword } from '../lib/auth'
import { registerPasskey } from '../lib/api/passkeys'
import { defaultPasskeyName, passkeysSupported } from '../lib/webauthn'
import { createSetupAdmin, getSetupStatus, type SetupStatus } from '../lib/api/meta'
import {
  canEmbedProvider,
  createAIProvider,
  isLLMProvider,
  listAIProviders,
  providerConfigured,
  recommendedModel,
  requiresAPIKey,
  requiresSignIn,
  sdkAliasDefault,
  keylessProviderDocs,
  keylessProviderHint,
  SDK_DEFAULT_BASE,
  SDK_OPTIONS,
  type AIProvider,
  type ModelPurpose,
  type ProviderSDK,
} from '../lib/api/providers'
import { getAppSettings, updateAppSettings } from '../lib/api/settings'
import { useAppMeta } from '../hooks/useAppMeta'
import { AppFooter } from './AppFooter'
import { ChatGPTSignIn } from './ChatGPTSignIn'
import { ProviderModelFields } from './ProviderModelFields'
import {
  AppLogo,
  Button,
  DocsLink,
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
  // The meta endpoint is public, which is what lets the wizard read the flag
  // before there is a session to read it with.
  const { chatgptLogin } = useAppMeta()
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
  // The guided form is the way in; the generic one below is the escape hatch
  // for anything it does not cover -- a ChatGPT sign-in, a local sidecar, a
  // second key on an instance that already has one.
  const [guided, setGuided] = useState(!initialStatus.provider_count)
  const [mistralKey, setMistralKey] = useState('')
  const [generalSdk, setGeneralSdk] = useState<ProviderSDK>('opencode')
  const [generalKey, setGeneralKey] = useState('')
  const [sdk, setSdk] = useState<ProviderSDK>('openai')
  const [alias, setAlias] = useState('')
  const [baseURL, setBaseURL] = useState(SDK_DEFAULT_BASE.openai)
  const [apiKey, setApiKey] = useState('')
  // The row a ChatGPT sign-in is waiting on. Signing in needs a provider id to
  // store the token against, so the SDK cannot be finished in one step: the row
  // is created first, then signed in to, and only then does the wizard move on.
  const [signInProvider, setSignInProvider] = useState<AIProvider | null>(null)

  const [ocrProviderId, setOcrProviderId] = useState('')
  const [ocrModel, setOcrModel] = useState('')
  const [extractProviderId, setExtractProviderId] = useState('')
  const [extractModel, setExtractModel] = useState('')
  const [embeddingProviderId, setEmbeddingProviderId] = useState('')
  const [embeddingModel, setEmbeddingModel] = useState('')

  useEffect(() => {
    if (step === 'admin') return

    let active = true
    async function load() {
      try {
        const [nextProviders, settings] = await Promise.all([listAIProviders(), getAppSettings()])
        if (!active) return
        setProviders(nextProviders)
        // Each binding falls back to a provider that can serve it and then to
        // the model the guide names for that provider's SDK, so the guided path
        // reaches this step with nothing left to choose. `recommendedModel`
        // answers for two SDKs and empty for the rest, which leaves the picker
        // to ask as it always did.
        const byId = (id: string) => nextProviders.find((item) => item.id === id)
        // A provider we can name a model for wins the job it has one for --
        // which is how the guided pair sorts itself out: Mistral takes OCR and
        // embeddings, and the other key is left to do the thinking.
        const named = (purpose: ModelPurpose) =>
          nextProviders.find((item) => recommendedModel(item.sdk, purpose))
        const ocr = byId(settings.ocr_provider_id) ?? named('ocr') ?? nextProviders[0]
        setOcrProviderId(ocr?.id ?? '')
        setOcrModel(settings.ocr_model || recommendedModel(ocr?.sdk, 'ocr'))
        const llmProviders = nextProviders.filter((item) => isLLMProvider(item.sdk))
        const llm =
          byId(settings.extract_provider_id) ??
          // Not the row OCR just took, where there is another: Mistral serves
          // both, so the first LLM row is the OCR one, and the language model
          // the operator added a second key for would never be offered.
          llmProviders.find((item) => item.id !== ocr?.id) ??
          llmProviders[0]
        setExtractProviderId(llm?.id ?? '')
        setExtractModel(settings.extract_model || recommendedModel(llm?.sdk, 'llm'))
        const embed =
          byId(settings.embedding_provider_id) ??
          named('embedding') ??
          nextProviders.find((item) => canEmbedProvider(item.sdk))
        setEmbeddingProviderId(embed?.id ?? '')
        setEmbeddingModel(settings.embedding_model || recommendedModel(embed?.sdk, 'embedding'))
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

  // The two keys of docs/guided_ai_setup.html in one submit: Mistral for OCR
  // and embeddings, one other provider for the language model. Either half may
  // be left out -- a Mistral key alone is a complete install, and an instance
  // that already has one only needs the other.
  async function onSaveGuided(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setSubmitting(true)
      setError('')
      const wanted = [
        { sdk: 'mistral' as ProviderSDK, key: mistralKey.trim() },
        { sdk: generalSdk, key: generalKey.trim() },
      ].filter((item) => item.key)
      if (wanted.length === 0) {
        throw new Error('Enter at least one API key.')
      }
      for (const item of wanted) {
        // An SDK already added is left alone rather than added twice: this
        // submit creates two rows, so a failure on the second one would
        // otherwise duplicate the first on the retry.
        if (providers.some((existing) => existing.sdk === item.sdk)) continue
        await createAIProvider({
          sdk: item.sdk,
          alias: sdkAliasDefault(item.sdk),
          base_url: SDK_DEFAULT_BASE[item.sdk],
          api_key: item.key,
        })
      }
      setProviders(await listAIProviders())
      setMistralKey('')
      setGeneralKey('')
      const next = await refreshStatus()
      setStep(next.needs_config ? 'models' : 'done')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save providers')
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
      const created = await createAIProvider({
        sdk,
        alias: alias.trim() || sdkAliasDefault(sdk),
        base_url: baseURL.trim(),
        api_key: apiKey.trim(),
      })
      const nextProviders = await listAIProviders()
      setProviders(nextProviders)
      setApiKey('')
      setAlias('')
      // A row that signs in is not usable yet, and the models step would offer
      // a provider that answers nothing. Hold here until the token is stored.
      if (requiresSignIn(sdk)) {
        setSignInProvider(nextProviders.find((item) => item.id === created.id) ?? created)
        return
      }
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

  // Called by the sign-in panel whenever it stores or clears a token. It is the
  // provider list that says whether the sign-in took, not the panel: the token
  // never reaches the browser, so `signed_in` on the reloaded row is the only
  // evidence there is.
  async function onSignedIn() {
    const nextProviders = await listAIProviders()
    setProviders(nextProviders)
    const row = signInProvider && nextProviders.find((item) => item.id === signInProvider.id)
    if (!row) {
      setSignInProvider(null)
      return
    }
    setSignInProvider(row)
    if (!providerConfigured(row)) return
    setSignInProvider(null)
    const next = await refreshStatus()
    setStep(next.needs_config ? 'models' : 'done')
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
        throw new Error('Choose an extraction provider.')
      }
      // The same rule the settings endpoint enforces, asked here so the answer
      // is a field to fill rather than a 400.
      if (embeddingProviderId && !embeddingModel.trim()) {
        throw new Error('Choose an embedding model, or set the embedding provider to None.')
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
        // Optional, unlike the two above: empty clears the binding and Deep
        // Search runs on keywords alone, which is what every install did before
        // embeddings existed.
        embedding_provider_id: embeddingProviderId,
        embedding_model: embeddingProviderId ? embeddingModel : '',
      })
      const next = await refreshStatus()
      if (next.needs_config) {
        if (!next.has_ocr || !next.has_llm) {
          setStep(next.provider_count ? 'models' : 'providers')
        }
        setError('Setup is still incomplete. Add an OCR provider and a language-model provider.')
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
          ? '2 · Providers'
          : step === 'models'
            ? '3 · Models'
            : 'Ready'

  const llmProviders = providers.filter((item) => isLLMProvider(item.sdk))
  const keylessDocs = keylessProviderDocs(sdk)

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
            {/* The sign-in stands on a row that is already added, so the
                heading follows what the step is actually asking for. */}
            {step === 'providers' &&
              (signInProvider
                ? 'Sign in to ChatGPT'
                : guided
                  ? 'Connect your AI providers'
                  : 'Add a provider')}
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

          {step === 'providers' && signInProvider && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-ink-muted">
                <strong className="font-medium text-ink">{signInProvider.alias}</strong> is saved
                but not signed in yet. Open the link below, enter the code, and setup carries on by
                itself once the token is stored.
              </p>
              <ChatGPTSignIn provider={signInProvider} onChange={onSignedIn} />
              {error && <p className="text-sm text-madder">{error}</p>}
              <button
                type="button"
                className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                onClick={() => setSignInProvider(null)}
              >
                Add a different provider instead
              </button>
            </div>
          )}

          {step === 'providers' && !signInProvider && guided && (
            <form className="flex flex-col gap-4" onSubmit={onSaveGuided}>
              <p className="text-sm text-ink-muted">
                Two keys cover everything: <strong className="font-medium text-ink">Mistral</strong>{' '}
                reads your documents and powers meaning-based search, and one other provider does
                the thinking — extraction, chat and Deep Search.
              </p>
              <p className={fieldHintClassName}>
                No provider account yet?{' '}
                <DocsLink href="/docs/guided_ai_setup.html">
                  Follow the guided AI provider setup
                </DocsLink>{' '}
                — it walks through both, and the Mistral key is free.
              </p>
              {providers.length > 0 && (
                <p className="text-xs text-ink-soft">
                  Already added: {providers.map((item) => item.alias).join(', ')}
                </p>
              )}
              <label className={labelClassName}>
                <span className={labelTextClassName}>Mistral API key — reading and retrieval</span>
                <input
                  type="password"
                  autoComplete="off"
                  value={mistralKey}
                  onChange={(e) => setMistralKey(e.target.value)}
                  className={inputClassName}
                />
              </label>
              <p className={fieldHintClassName}>
                OCR and embeddings, both on the free tier. Mistral can run the language model too,
                so this key alone is a complete install.
              </p>
              <label className={labelClassName}>
                <span className={labelTextClassName}>General AI provider</span>
                <select
                  value={generalSdk}
                  onChange={(e) => setGeneralSdk(e.target.value as ProviderSDK)}
                  className={inputClassName}
                >
                  {/* Every SDK that can run the language model, except the two
                      this form cannot ask for in one submit: mistral is the
                      field above, and chatgpt is signed in to rather than
                      given a key -- both reachable through the manual form. */}
                  {SDK_OPTIONS.filter(
                    (option) =>
                      isLLMProvider(option.value) &&
                      option.value !== 'mistral' &&
                      option.value !== 'chatgpt',
                  ).map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{sdkAliasDefault(generalSdk)} API key</span>
                <input
                  type="password"
                  autoComplete="off"
                  value={generalKey}
                  onChange={(e) => setGeneralKey(e.target.value)}
                  className={inputClassName}
                />
              </label>
              <p className={fieldHintClassName}>Leave blank to run everything on Mistral.</p>
              {error && <p className="text-sm text-madder">{error}</p>}
              <div className="flex flex-col gap-2">
                <Button type="submit" disabled={submitting}>
                  {submitting ? 'Saving...' : 'Continue'}
                </Button>
                <button
                  type="button"
                  className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                  onClick={() => {
                    setError('')
                    setGuided(false)
                  }}
                >
                  Add a provider manually instead
                </button>
              </div>
            </form>
          )}

          {step === 'providers' && !signInProvider && !guided && (
            <form className="flex flex-col gap-4" onSubmit={onSaveProvider}>
              <p className="text-sm text-ink-muted">
                Add a provider. OpenAI, OpenRouter, or Mistral can run extraction and chat;
                Google Vision or Mistral OCR can run OCR, and one Mistral provider covers both.
                Local OCR runs Docling on your own host and Local Embeddings serves Deep
                Search's dense half — neither needs an API key, but each needs its compose
                overlay running first.
                {chatgptLogin === true && (
                  <>
                    {' '}
                    A ChatGPT subscription covers extraction, chat and OCR on the seat you already
                    pay for, with no API key at all — you sign in with a code instead.
                  </>
                )}
              </p>
              {/* The step asks for a key from an account the operator may not
                  have opened yet, which is the one thing no hint here can
                  supply. The guide is the walk-through for that. */}
              <p className={fieldHintClassName}>
                No provider account yet?{' '}
                <DocsLink href="/docs/guided_ai_setup.html">
                  Follow the guided AI provider setup
                </DocsLink>{' '}
                — a free Mistral key for OCR, Opencode Go for the language model.
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
                  {/* chatgpt only where the instance opted in, the same
                      condition Settings uses. Signing in needs a saved row to
                      store the token against, so choosing it here creates the
                      row and then holds the step open for the sign-in rather
                      than finishing in one submit. */}
                  {SDK_OPTIONS.filter(
                    (option) => option.value !== 'chatgpt' || chatgptLogin === true,
                  ).map((option) => (
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
                  placeholder={sdkAliasDefault(sdk)}
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
                <p className={fieldHintClassName}>
                  {keylessProviderHint(sdk)}{' '}
                  {keylessDocs && (
                    <DocsLink href={keylessDocs.href}>Read the {keylessDocs.label} guide.</DocsLink>
                  )}
                </p>
              )}
              {error && <p className="text-sm text-madder">{error}</p>}
              <div className="flex flex-col gap-2">
                <Button type="submit" disabled={submitting}>
                  {submitting ? 'Saving...' : 'Continue'}
                </Button>
                <button
                  type="button"
                  className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                  onClick={() => {
                    setError('')
                    setGuided(true)
                  }}
                >
                  Back to the guided setup
                </button>
              </div>
            </form>
          )}

          {step === 'models' && (
            <form className="flex flex-col gap-4" onSubmit={onSaveModels}>
              <p className="text-sm text-ink-muted">
                Pick a provider and model for OCR and metadata extraction. Chat and search are set
                to the extraction model too, and embeddings are optional; you can change them
                later in Settings.
              </p>
              {llmProviders.length === 0 && (
                <p className="text-sm text-amber-800">
                  Add an OpenAI, OpenRouter{chatgptLogin === true ? ', ChatGPT' : ''} or Mistral
                  provider to enable extraction and chat.
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
              <ProviderModelFields
                label="Embeddings"
                help="Lets Deep Search find documents by meaning as well as by keyword, so a question in one language reaches a document written in another. Set the provider to None to search by keyword only; turning it on embeds the whole archive, not only new uploads."
                providers={providers}
                providerId={embeddingProviderId}
                model={embeddingModel}
                purpose="embedding"
                allowEmpty
                onProviderChange={setEmbeddingProviderId}
                onModelChange={setEmbeddingModel}
              />
              {error && <p className="text-sm text-madder">{error}</p>}
              <div className="flex flex-col gap-2">
                <Button type="submit" disabled={submitting}>
                  {submitting ? 'Saving...' : 'Finish setup'}
                </Button>
                <button
                  type="button"
                  className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                  onClick={() => {
                    // Straight to the manual form: the guided pair is what got
                    // us here, so whatever is still missing is one of the
                    // things it does not cover.
                    setGuided(false)
                    setError('')
                    setStep('providers')
                  }}
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
