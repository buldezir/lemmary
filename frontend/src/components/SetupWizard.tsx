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
  usesOCRModel,
  providerDocs,
  keylessProviderHint,
  SDK_DEFAULT_BASE,
  SDK_OPTIONS,
  type AIProvider,
  type ModelPurpose,
  type ProviderSDK,
} from '../lib/api/providers'
import { getAppSettings, updateAppSettings } from '../lib/api/settings'
import { docsUrl, t, tNode } from '../i18n'
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

// Deliberately never returns 'passkey': the optional step is reachable only
// from the in-session transition out of 'admin', which is what stops it
// resurfacing on a later boot or on a wizard that resumes at 'providers'.
function initialStep(status: SetupStatus): Step {
  if (status.needs_admin) return 'admin'
  return nextConfigStep(status)
}

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

  // Stashed rather than re-derived, so skip and finish land in the same place
  // without a second round trip.
  const [afterPasskey, setAfterPasskey] = useState<Step>('done')
  const [passkeyName, setPasskeyName] = useState('')

  const [providers, setProviders] = useState<AIProvider[]>([])
  // The guided form is the way in; the generic one below is the escape hatch
  // for anything it does not cover.
  const [guided, setGuided] = useState(!initialStatus.provider_count)
  const [mistralKey, setMistralKey] = useState('')
  const [generalSdk, setGeneralSdk] = useState<ProviderSDK>('opencode')
  const [generalKey, setGeneralKey] = useState('')
  const [sdk, setSdk] = useState<ProviderSDK>('openai')
  const [alias, setAlias] = useState('')
  const [baseURL, setBaseURL] = useState(SDK_DEFAULT_BASE.openai)
  const [apiKey, setApiKey] = useState('')
  // The row a ChatGPT sign-in is waiting on: the token needs a provider id to
  // be stored against, so the row is created before the sign-in.
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
        // the model the guide names for its SDK, so the guided path reaches
        // this step with nothing left to choose.
        const byId = (id: string) => nextProviders.find((item) => item.id === id)
        // A provider we can name a model for wins the job it has one for, which
        // is how the guided pair sorts itself out.
        const named = (purpose: ModelPurpose) =>
          nextProviders.find((item) => recommendedModel(item.sdk, purpose))
        // A saved model belongs to the provider it was saved against. Where that
        // row is gone, the fallback provider gets the guide's model for its SDK,
        // never the orphaned string the picker has no catalogue entry for.
        const saved = (id: string, model: string, provider?: AIProvider) =>
          provider && provider.id === id ? model : ''
        const ocr = byId(settings.ocr_provider_id) ?? named('ocr') ?? nextProviders[0]
        setOcrProviderId(ocr?.id ?? '')
        setOcrModel(
          saved(settings.ocr_provider_id, settings.ocr_model, ocr) ||
            recommendedModel(ocr?.sdk, 'ocr'),
        )
        const llmProviders = nextProviders.filter((item) => isLLMProvider(item.sdk))
        const llm =
          byId(settings.extract_provider_id) ??
          // Not the row OCR just took, where there is another: Mistral serves
          // both, so the second key would otherwise never be offered.
          llmProviders.find((item) => item.id !== ocr?.id) ??
          llmProviders[0]
        setExtractProviderId(llm?.id ?? '')
        setExtractModel(
          saved(settings.extract_provider_id, settings.extract_model, llm) ||
            recommendedModel(llm?.sdk, 'llm'),
        )
        const embed =
          byId(settings.embedding_provider_id) ??
          named('embedding') ??
          nextProviders.find((item) => canEmbedProvider(item.sdk))
        setEmbeddingProviderId(embed?.id ?? '')
        setEmbeddingModel(
          saved(settings.embedding_provider_id, settings.embedding_model, embed) ||
            recommendedModel(embed?.sdk, 'embedding'),
        )
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
        throw new Error(t('setupWizard.passwordMismatch'))
      }
      await createSetupAdmin(email.trim(), password, passwordConfirm)
      await loginWithPassword(email.trim(), password)
      const next = await refreshStatus()
      const target = nextConfigStep(next)
      setAfterPasskey(target)
      // Offered only where it can work: an install reached over plain HTTP
      // cannot create a passkey, and a dead end here is worse than not asking.
      if (passkeysSupported()) {
        setPasskeyName(defaultPasskeyName())
        setStep('passkey')
        return
      }
      setStep(target)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('setupWizard.createAdminFailed'))
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
      setError(err instanceof Error ? err.message : t('setupWizard.passkeyFailed'))
    } finally {
      setSubmitting(false)
    }
  }

  // The two keys of docs/guided_ai_setup.html in one submit. Either half may be
  // left out: a Mistral key alone is a complete install.
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
        throw new Error(t('setupWizard.enterOneKey'))
      }
      // Read from the server rather than from state: a failure on the second row
      // leaves the first saved but unknown here, and the retry would be refused
      // for an alias that now exists.
      const already = await listAIProviders()
      for (const item of wanted) {
        // An SDK already added is left alone rather than added twice.
        if (already.some((existing) => existing.sdk === item.sdk)) continue
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
      setError(err instanceof Error ? err.message : t('setupWizard.saveProvidersFailed'))
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
        throw new Error(t('setupWizard.enterKey'))
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
      // A row that signs in is not usable yet, so hold here until the token is
      // stored rather than offer a provider that answers nothing.
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
      setError(err instanceof Error ? err.message : t('providersBlock.saveFailed'))
    } finally {
      setSubmitting(false)
    }
  }

  // The provider list says whether the sign-in took, not the panel: the token
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
        throw new Error(t('setupWizard.chooseOcrProvider'))
      }
      if (!extractProviderId) {
        throw new Error(t('setupWizard.chooseExtractionProvider'))
      }
      // The rules the settings endpoint enforces, asked here so the answer is a
      // field to fill rather than a 400 quoting a request field. The two
      // modelless OCR SDKs are exempt, the same pair the picker hides the model
      // box for.
      const ocrSdk = providers.find((item) => item.id === ocrProviderId)?.sdk
      if (usesOCRModel(ocrSdk) && !ocrModel.trim()) {
        throw new Error(t('setupWizard.chooseOcrModel'))
      }
      if (!extractModel.trim()) {
        throw new Error(t('setupWizard.chooseExtractionModel'))
      }
      if (embeddingProviderId && !embeddingModel.trim()) {
        throw new Error(t('setupWizard.chooseEmbeddingModel'))
      }
      // First-launch setup asks for one LLM binding; Deep Research's own model
      // can be set later in Settings.
      await updateAppSettings({
        ocr_provider_id: ocrProviderId,
        ocr_model: ocrModel,
        extract_provider_id: extractProviderId,
        extract_model: extractModel,
        // Optional, unlike the two above: empty clears the binding and Deep
        // Search runs on keywords alone.
        embedding_provider_id: embeddingProviderId,
        embedding_model: embeddingProviderId ? embeddingModel : '',
      })
      const next = await refreshStatus()
      if (next.needs_config) {
        if (!next.has_ocr || !next.has_llm) {
          setStep(next.provider_count ? 'models' : 'providers')
        }
        setError(t('setupWizard.stillIncomplete'))
        return
      }
      setStep('done')
    } catch (err) {
      setError(err instanceof Error ? err.message : t('setupWizard.saveModelsFailed'))
    } finally {
      setSubmitting(false)
    }
  }

  const stepLabel =
    step === 'admin'
      ? t('setupWizard.stepAdmin')
      : step === 'passkey'
        ? t('setupWizard.stepPasskey')
        : step === 'providers'
          ? t('setupWizard.stepProviders')
          : step === 'models'
            ? t('setupWizard.stepModels')
            : t('setupWizard.stepReady')

  const llmProviders = providers.filter((item) => isLLMProvider(item.sdk))
  const keylessDocs = providerDocs(sdk)

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
            {step === 'admin' && t('setupWizard.headingAdmin')}
            {step === 'passkey' && t('setupWizard.headingPasskey')}
            {step === 'providers' &&
              (signInProvider
                ? t('setupWizard.headingSignIn')
                : guided
                  ? t('setupWizard.headingGuided')
                  : t('setupWizard.headingProvider'))}
            {step === 'models' && t('setupWizard.headingModels')}
            {step === 'done' && t('setupWizard.headingDone')}
          </h2>

          {step === 'admin' && (
            <form className="flex flex-col gap-4" onSubmit={onCreateAdmin}>
              <p className="text-sm text-ink-muted">
                {t('setupWizard.adminIntro')}
              </p>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('loginPage.email')}</span>
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
                <span className={labelTextClassName}>{t('loginPage.password')}</span>
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
                <span className={labelTextClassName}>{t('setupWizard.confirmPassword')}</span>
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
                {submitting ? t('setupWizard.creating') : t('setupWizard.createAdmin')}
              </Button>
            </form>
          )}

          {step === 'passkey' && (
            <form className="flex flex-col gap-4" onSubmit={onAddPasskey}>
              <p className="text-sm text-ink-muted">
                {t('setupWizard.passkeyIntro')}
              </p>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('setupWizard.name')}</span>
                <input
                  value={passkeyName}
                  onChange={(e) => setPasskeyName(e.target.value)}
                  className={inputClassName}
                />
              </label>
              {error && <p className="text-sm text-madder">{error}</p>}
              <Button type="submit" disabled={submitting}>
                {submitting ? t('setupWizard.waitingDevice') : t('setupWizard.createPasskey')}
              </Button>
              {/* Never disabled: a failure on this step must not trap anyone in
                  an optional detour. */}
              <button
                type="button"
                className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                onClick={() => setStep(afterPasskey)}
              >
                {t('setupWizard.skip')}
              </button>
            </form>
          )}

          {step === 'providers' && signInProvider && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-ink-muted">
                {tNode('setupWizard.signInIntro', {
                  alias: <strong className="font-medium text-ink">{signInProvider.alias}</strong>,
                })}
              </p>
              <ChatGPTSignIn provider={signInProvider} onChange={onSignedIn} />
              {error && <p className="text-sm text-madder">{error}</p>}
              <button
                type="button"
                className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                onClick={() => setSignInProvider(null)}
              >
                {t('setupWizard.differentProvider')}
              </button>
            </div>
          )}

          {step === 'providers' && !signInProvider && guided && (
            <form className="flex flex-col gap-4" onSubmit={onSaveGuided}>
              <p className="text-sm text-ink-muted">
                {tNode('setupWizard.guidedIntro', {
                  mistral: <strong className="font-medium text-ink">Mistral</strong>,
                })}
              </p>
              <p className={fieldHintClassName}>
                {tNode('setupWizard.guidedNoAccount', {
                  link: (
                    <DocsLink href={docsUrl('guided_ai_setup.html')}>
                      {t('setupWizard.guidedLink')}
                    </DocsLink>
                  ),
                })}
              </p>
              {providers.length > 0 && (
                <p className="text-xs text-ink-soft">
                  {t('setupWizard.alreadyAdded', {
                    names: providers.map((item) => item.alias).join(', '),
                  })}
                </p>
              )}
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('setupWizard.mistralKey')}</span>
                <input
                  type="password"
                  autoComplete="off"
                  value={mistralKey}
                  onChange={(e) => setMistralKey(e.target.value)}
                  className={inputClassName}
                />
              </label>
              <p className={fieldHintClassName}>
                {t('setupWizard.mistralHint')}
              </p>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('setupWizard.generalProvider')}</span>
                <select
                  value={generalSdk}
                  onChange={(e) => setGeneralSdk(e.target.value as ProviderSDK)}
                  className={inputClassName}
                >
                  {/* The two this form cannot ask for in one submit are left out:
                      mistral is the field above, chatgpt is signed in to rather
                      than given a key. Both are in the manual form. */}
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
                <span className={labelTextClassName}>
                  {t('setupWizard.providerKey', { name: sdkAliasDefault(generalSdk) })}
                </span>
                <input
                  type="password"
                  autoComplete="off"
                  value={generalKey}
                  onChange={(e) => setGeneralKey(e.target.value)}
                  className={inputClassName}
                />
              </label>
              <p className={fieldHintClassName}>{t('setupWizard.generalHint')}</p>
              {error && <p className="text-sm text-madder">{error}</p>}
              <div className="flex flex-col gap-2">
                <Button type="submit" disabled={submitting}>
                  {submitting ? t('settingsFeedback.saving') : t('setupWizard.continue')}
                </Button>
                <button
                  type="button"
                  className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                  onClick={() => {
                    setError('')
                    setGuided(false)
                  }}
                >
                  {t('setupWizard.manual')}
                </button>
              </div>
            </form>
          )}

          {step === 'providers' && !signInProvider && !guided && (
            <form className="flex flex-col gap-4" onSubmit={onSaveProvider}>
              <p className="text-sm text-ink-muted">
                {t('setupWizard.manualIntro')}
              </p>
              <p className={fieldHintClassName}>
                {tNode('setupWizard.manualNoAccount', {
                  link: (
                    <DocsLink href={docsUrl('guided_ai_setup.html')}>
                      {t('setupWizard.guidedLink')}
                    </DocsLink>
                  ),
                })}
              </p>
              {providers.length > 0 && (
                <p className="text-xs text-ink-soft">
                  {t('setupWizard.alreadyAdded', {
                    names: providers.map((item) => item.alias).join(', '),
                  })}
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
                  {/* Choosing chatgpt creates the row and holds the step open
                      for the sign-in. */}
                  {SDK_OPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('providersBlock.alias')}</span>
                <input
                  value={alias}
                  placeholder={sdkAliasDefault(sdk)}
                  onChange={(e) => setAlias(e.target.value)}
                  className={inputClassName}
                />
              </label>
              {sdk !== 'google_vision' && (
                <label className={labelClassName}>
                  <span className={labelTextClassName}>{t('providersBlock.baseUrl')}</span>
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
                  <span className={labelTextClassName}>{t('providersBlock.apiKey')}</span>
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
                  <DocsLink href={keylessDocs.href}>
                    {t('providersBlock.readGuide', { label: keylessDocs.label })}
                  </DocsLink>
                </p>
              )}
              {error && <p className="text-sm text-madder">{error}</p>}
              <div className="flex flex-col gap-2">
                <Button type="submit" disabled={submitting}>
                  {submitting ? t('settingsFeedback.saving') : t('setupWizard.continue')}
                </Button>
                <button
                  type="button"
                  className="text-left text-xs font-medium text-ink-soft hover:text-ink"
                  onClick={() => {
                    setError('')
                    setGuided(true)
                  }}
                >
                  {t('setupWizard.backToGuided')}
                </button>
              </div>
            </form>
          )}

          {step === 'models' && (
            <form className="flex flex-col gap-4" onSubmit={onSaveModels}>
              <p className="text-sm text-ink-muted">
                {t('setupWizard.modelsIntro')}
              </p>
              {llmProviders.length === 0 && (
                <p className="text-sm text-amber-800">
                  {t('setupWizard.noLlm')}
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
                label={t('bindingOverride.extraction')}
                providers={llmProviders}
                providerId={extractProviderId}
                model={extractModel}
                purpose="llm"
                onProviderChange={setExtractProviderId}
                onModelChange={setExtractModel}
              />
              <ProviderModelFields
                label={t('setupWizard.embeddings')}
                help={t('setupWizard.embeddingsHelp')}
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
                  {submitting ? t('settingsFeedback.saving') : t('setupWizard.finish')}
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
                  {t('setupWizard.addAnother')}
                </button>
              </div>
            </form>
          )}

          {step === 'done' && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-ink-muted">
                {t('setupWizard.doneIntro')}
              </p>
              {status.needs_config && (
                <p className="text-sm text-madder">{t('setupWizard.missingConfig')}</p>
              )}
              <Button onClick={onComplete}>{t('setupWizard.open', { appName })}</Button>
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
          <h2 className="mb-2 font-display text-lg font-semibold text-ink">
            {t('setupWizard.blockedTitle')}
          </h2>
          <p className="mb-4 text-sm text-ink-muted">
            {t('setupWizard.blockedIntro')}
          </p>
          <Button onClick={onLogout}>{t('rootLayout.logOut')}</Button>
        </section>
      </div>
      <AppFooter />
    </div>
  )
}
