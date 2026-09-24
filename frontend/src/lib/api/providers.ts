import { apiFetch } from '../apiClient'

export type ProviderSDK =
  | 'openai'
  | 'anthropic'
  | 'openrouter'
  | 'google_vision'
  | 'mistral'
  | 'opencode'
  | 'chatgpt'
  | 'local'
  | 'docling'
  | 'tavily'

export type AIProvider = {
  id: string
  sdk: ProviderSDK
  alias: string
  base_url: string
  /**
   * Which model catalogue answers for this provider's context windows. Empty
   * means no window is known, which costs the denominator on the usage line
   * and nothing else.
   */
  catalog: string
  api_key_set: boolean
  /** api_key_set's counterpart for the SDKs that sign in instead of taking a key. */
  signed_in: boolean
  /** Email or account id of the signed-in ChatGPT account. Never a token. */
  account?: string
  plan?: string
}

export type AIProviderWrite = {
  sdk: ProviderSDK
  alias: string
  base_url?: string
  api_key?: string
  catalog?: string
}

/**
 * The model catalogues the server accepts, mirroring aiprovider.CatalogProviders.
 * Hardcoded on both sides: it names a dropdown's options, and a list that has to
 * be fetched before a provider can be saved would put a third party's uptime in
 * the way of configuring one.
 */
export const MODEL_CATALOGS = [
  'amazon-bedrock',
  'ant-ling',
  'anthropic',
  'azure-openai-responses',
  'baseten',
  'cerebras',
  'cloudflare-ai-gateway',
  'cloudflare-workers-ai',
  'deepseek',
  'fireworks',
  'github-copilot',
  'google',
  'google-vertex',
  'groq',
  'huggingface',
  'kimi-coding',
  'minimax',
  'minimax-cn',
  'mistral',
  'moonshotai',
  'moonshotai-cn',
  'nvidia',
  'openai',
  'openai-codex',
  'opencode',
  'opencode-go',
  'openrouter',
  'qwen-token-plan',
  'qwen-token-plan-cn',
  'qwen-token-plan-individual',
  'together',
  'vercel-ai-gateway',
  'xai',
  'xiaomi',
  'xiaomi-token-plan-ams',
  'xiaomi-token-plan-cn',
  'xiaomi-token-plan-sgp',
  'zai',
  'zai-coding-cn',
] as const

const SDK_DEFAULT_CATALOG: Partial<Record<ProviderSDK, string>> = {
  openai: 'openai',
  anthropic: 'anthropic',
  openrouter: 'openrouter',
  mistral: 'mistral',
  opencode: 'opencode-go',
  chatgpt: 'openai-codex',
}

/** The catalogue an SDK is most likely served by; the admin can correct it. */
export function defaultCatalog(sdk: ProviderSDK): string {
  return SDK_DEFAULT_CATALOG[sdk] ?? ''
}

export type ModelPurpose = 'ocr' | 'llm' | 'embedding' | 'websearch'

/**
 * Mirrors aiprovider.Binding. Both halves in practice: the server refuses a
 * half-filled pair rather than filling it from Settings, whose model belongs to
 * a different provider. The exception is OCR on google_vision and docling,
 * which read a document without a model -- see `usesOCRModel`.
 */
export type ProviderBinding = {
  provider_id: string
  model: string
}

export const EMPTY_BINDING: ProviderBinding = { provider_id: '', model: '' }

export function bindingIsEmpty(binding: ProviderBinding | undefined) {
  return !binding?.provider_id.trim()
}

/**
 * The request fields for a binding, absent rather than empty strings so an
 * untouched picker sends what it sent before overrides existed.
 */
export function bindingBody(binding: ProviderBinding | undefined) {
  if (bindingIsEmpty(binding)) return {}
  return { provider_id: binding!.provider_id.trim(), model: binding!.model.trim() }
}

export type CatalogModel = {
  id: string
  name: string
  /** Context length in tokens, when the provider reports one (OpenAI does not). */
  context_window?: number
}

/** Must stay identical to aiprovider.DefaultBaseURL in backend/internal/aiprovider/sdk.go. */
export const SDK_DEFAULT_BASE: Record<ProviderSDK, string> = {
  openai: 'https://api.openai.com/v1',
  anthropic: 'https://api.anthropic.com/v1',
  openrouter: 'https://openrouter.ai/api/v1',
  mistral: 'https://api.mistral.ai/v1',
  opencode: 'https://opencode.ai/zen/go/v1',
  google_vision: '',
  // Not a /v1 root: the Codex backend serves one endpoint, and the middleware
  // behind this SDK rewrites the SDK's /chat/completions into it.
  chatgpt: 'https://chatgpt.com/backend-api/codex',
  // The sidecar service names from the compose overlays.
  local: 'http://embeddings:80/v1',
  docling: 'http://docling:5001',
  tavily: 'https://api.tavily.com',
}

export const SDK_OPTIONS: { value: ProviderSDK; label: string }[] = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'mistral', label: 'Mistral' },
  { value: 'opencode', label: 'Opencode Go' },
  { value: 'google_vision', label: 'Google Cloud Vision' },
  { value: 'chatgpt', label: 'ChatGPT subscription' },
  { value: 'local', label: 'Local Embeddings' },
  { value: 'docling', label: 'Local OCR' },
  { value: 'tavily', label: 'Tavily' },
]

export function sdkLabel(sdk: ProviderSDK | string) {
  return SDK_OPTIONS.find((option) => option.value === sdk)?.label ?? sdk
}

/**
 * Mirrors aiprovider.DefaultAlias. Not sdkLabel: stored as an alias, that reads
 * back through providerOptionLabel as `alias (sdk label)`, nesting its own
 * parentheses inside the label's.
 */
export function sdkAliasDefault(sdk: ProviderSDK | string) {
  if (sdk === 'local') return 'Local embeddings'
  if (sdk === 'docling') return 'Docling'
  return sdkLabel(sdk)
}

export function isLLMProvider(sdk: string) {
  return (
    sdk === 'openai' ||
    sdk === 'anthropic' ||
    sdk === 'openrouter' ||
    sdk === 'mistral' ||
    sdk === 'opencode' ||
    sdk === 'chatgpt'
  )
}

/**
 * Mirrors aiprovider.RequiresOAuth: the one SDK whose credential is minted by
 * signing in, so the form shows a sign-in panel instead of a key field.
 */
export function requiresSignIn(sdk?: string) {
  return sdk === 'chatgpt'
}

/**
 * Mirrors aiprovider.Provider.Configured, including the order it asks in: a
 * signed-in SDK by its token, a hosted one by its key, a sidecar by its address.
 */
export function providerConfigured(
  item: Pick<AIProvider, 'sdk' | 'api_key_set' | 'signed_in' | 'base_url'>,
) {
  if (requiresSignIn(item.sdk)) return item.signed_in
  if (requiresAPIKey(item.sdk)) return item.api_key_set
  return item.base_url.trim() !== ''
}

/**
 * Mirrors aiprovider.CanEmbed. Deliberately not isLLMProvider: `local` embeds
 * without chatting, and anthropic, chatgpt and opencode chat without embedding
 * because none of those endpoints serves /embeddings.
 */
export function canEmbedProvider(sdk: string) {
  return (
    (isLLMProvider(sdk) && sdk !== 'chatgpt' && sdk !== 'opencode' && sdk !== 'anthropic') ||
    sdk === 'local'
  )
}

/**
 * Mirrors aiprovider.RequiresAPIKey. The sidecars are reached by address alone
 * and chatgpt signs in instead. Default-true, so an unknown SDK still asks.
 */
export function requiresAPIKey(sdk?: string) {
  return sdk !== 'local' && sdk !== 'docling' && sdk !== 'chatgpt'
}

/**
 * Mirrors aiprovider.CanWebSearch. An allow-list, like canEmbedProvider: no SDK
 * that chats, embeds or reads a document also searches the web.
 */
export function canWebSearchProvider(sdk?: string) {
  return sdk === 'tavily'
}

/** Which SDKs may be bound to a given task, for the provider pickers. */
export function providerServesPurpose(sdk: string, purpose: ModelPurpose) {
  if (purpose === 'embedding') return canEmbedProvider(sdk)
  if (purpose === 'llm') return isLLMProvider(sdk)
  if (purpose === 'websearch') return canWebSearchProvider(sdk)
  // Mirrors aiprovider.CanOCR: every SDK but a local embeddings endpoint and a
  // web-search API can serve OCR by sending the file to a model. Default-true,
  // so a new SDK that cannot read a document has to be named here -- otherwise
  // it binds happily and fails on the first upload.
  return sdk !== 'local' && sdk !== 'tavily'
}

/**
 * `providerServesPurpose` plus the one already bound, kept whatever its SDK so
 * an existing binding never renders as blank. Exists so no call site
 * pre-filters its own list and strips the providers it was about to offer.
 */
export function eligibleProviders<T extends { id: string; sdk: string }>(
  providers: T[],
  purpose: ModelPurpose,
  boundId?: string,
) {
  return providers.filter(
    (item) => item.id === boundId || providerServesPurpose(item.sdk, purpose),
  )
}

/**
 * Mirrors aiprovider.RequiresOCRModel. Google Vision has no model to give; for
 * the sidecar, what looks like one is an optional OCR engine name.
 */
export function usesOCRModel(sdk?: string) {
  return sdk !== 'google_vision' && sdk !== 'docling'
}

/**
 * What the OCR "model" means for the local sidecar. Empty for every other SDK,
 * where the field really does name a model.
 */
export function localOCRModelHint(sdk?: string) {
  if (sdk !== 'docling') return ''
  return 'Optional. Names Docling\u2019s OCR engine \u2014 rapidocr (the default, PaddleOCR\u2019s PP-OCR models), easyocr, tesserocr or tesseract. An unrecognised name is ignored silently, so check the spelling.'
}

/**
 * The hint shown under a keyless provider's Base URL, in place of an API key.
 * Empty for the hosted SDKs, which show the key field instead.
 */
export function keylessProviderHint(sdk?: string) {
  // chatgpt needs no key either, but it is not keyless: the sign-in panel
  // stands where this hint would.
  if (requiresAPIKey(sdk) || requiresSignIn(sdk)) return ''
  const overlay = sdk === 'local' ? 'docker-compose.embeddings.yml' : 'docker-compose.local-ocr.yml'
  return `Runs on your own host, so no API key is needed \u2014 the address is the whole configuration. The default is the service name from ${overlay}.`
}

/**
 * The guide behind a keyless provider's hint; neither sidecar answers until its
 * compose overlay is up. Null for the hosted SDKs.
 *
 * The `.html` is load-bearing: VitePress has no cleanUrls and the static
 * handler in appwire/wire.go never tries an .html suffix, so a bare
 * /docs/local_ocr falls through to the SPA.
 */
export function keylessProviderDocs(sdk?: string) {
  if (sdk === 'local') {
    return { href: '/docs/local_embeddings.html', label: 'Local embeddings' }
  }
  if (sdk === 'docling') {
    return { href: '/docs/local_ocr.html', label: 'Local OCR' }
  }
  return null
}

/**
 * The model the guided setup binds, for the two SDKs whose ids can be named up
 * front (docs/guided_ai_setup.md). A suggestion, not a constraint: it only ever
 * fills a field nothing else has filled.
 */
export function recommendedModel(sdk: string | undefined, purpose: ModelPurpose) {
  if (sdk === 'mistral') {
    if (purpose === 'ocr') return 'mistral-ocr-latest'
    if (purpose === 'embedding') return 'mistral-embed'
    return ''
  }
  // Mirrors aiprovider.DefaultExtractModel.
  if (sdk === 'opencode' && purpose === 'llm') return 'gpt-5.6-luna'
  if (sdk === 'anthropic' && purpose !== 'embedding') return 'claude-opus-5'
  if (sdk === 'local' && purpose === 'embedding') return 'BAAI/bge-m3'
  return ''
}

export function providerOptionLabel(item: Pick<AIProvider, 'alias' | 'sdk'>) {
  const sdk = sdkLabel(item.sdk)
  return item.alias.toLowerCase() === sdk.toLowerCase() ? item.alias : `${item.alias} (${sdk})`
}

export function modelOptionLabel(item: CatalogModel) {
  if (item.name && item.name !== item.id) {
    return `${item.id} (${item.name})`
  }
  return item.id
}

export const OCR_MODEL_WARNING =
  'Choose this model wisely — this provider does not advertise which models accept file inputs.'

export function showsOCRModelWarning(sdk?: string) {
  return sdk === 'openai'
}

export async function listAIProviders() {
  const data = await apiFetch<{ providers?: AIProvider[] }>('/api/app/providers', {
    fallbackError: 'Failed to load providers',
  })
  return data.providers ?? []
}

export function createAIProvider(body: AIProviderWrite) {
  return apiFetch<AIProvider>('/api/app/providers', {
    method: 'POST',
    body,
    fallbackError: 'Failed to create provider',
  })
}

export function updateAIProvider(id: string, body: Partial<AIProviderWrite>) {
  return apiFetch<AIProvider>(`/api/app/providers/${id}`, {
    method: 'PATCH',
    body,
    fallbackError: 'Failed to update provider',
  })
}

export async function deleteAIProvider(id: string) {
  await apiFetch<{ detail?: string }>(`/api/app/providers/${id}`, {
    method: 'DELETE',
    fallbackError: 'Failed to delete provider',
  })
}

export async function listProviderModels(id: string, purpose: ModelPurpose = 'llm') {
  const data = await apiFetch<{ models?: CatalogModel[]; sdk?: string }>(
    `/api/app/providers/${id}/models?for=${purpose}`,
    { fallbackError: 'Failed to load models' },
  )
  return { models: data.models ?? [], sdk: data.sdk ?? '' }
}

export type ChatGPTDeviceLogin = {
  user_code: string
  verification_url: string
  interval_seconds: number
  expires_in: number
}

export type ChatGPTLoginStatus = {
  status: 'pending' | 'complete' | 'expired'
  provider?: AIProvider
}

/**
 * The device auth id stays on the server: it is the half that would let anyone
 * holding it finish somebody else's login.
 */
export function startChatGPTLogin(id: string) {
  return apiFetch<ChatGPTDeviceLogin>(`/api/app/providers/${id}/chatgpt/device`, {
    method: 'POST',
    fallbackError: 'Failed to start the ChatGPT sign-in',
  })
}

/**
 * Checks once whether the code has been approved. The caller drives the
 * interval, so an abandoned sign-in holds no connection open.
 */
export function pollChatGPTLogin(id: string) {
  return apiFetch<ChatGPTLoginStatus>(`/api/app/providers/${id}/chatgpt/device/poll`, {
    method: 'POST',
    fallbackError: 'Failed to check the ChatGPT sign-in',
  })
}

/** Clears the stored token. The provider row stays, ready for another account. */
export function signOutChatGPT(id: string) {
  return apiFetch<AIProvider>(`/api/app/providers/${id}/chatgpt`, {
    method: 'DELETE',
    fallbackError: 'Failed to sign out of ChatGPT',
  })
}

export type OCRProviderInfo = {
  id: string
  name: string
  sdk: string
}

export async function listOCRProviders() {
  const data = await apiFetch<{ providers?: OCRProviderInfo[] }>('/api/app/ocr/providers', {
    fallbackError: 'Failed to load OCR providers',
  })
  return data.providers ?? []
}

/** What answers when nobody overrides. Empty when nothing is bound yet. */
export type ConfiguredBinding = {
  provider_id?: string
  provider_name?: string
  model?: string
}

/**
 * For a picker outside Settings. Readable by any signed-in user, unlike the
 * admin-only `listAIProviders`: this answer carries no credential state, only
 * provider names, model ids and SDKs.
 */
export async function listPickableProviders(purpose: ModelPurpose, binding?: string) {
  const query = new URLSearchParams({ for: purpose })
  // Names which configured pair to report back: chat, search and extraction
  // are all language models, so the purpose alone cannot say.
  if (binding) query.set('binding', binding)
  const data = await apiFetch<{ providers?: OCRProviderInfo[]; configured?: ConfiguredBinding }>(
    `/api/app/ai/providers?${query.toString()}`,
    { fallbackError: 'Failed to load providers' },
  )
  return { providers: data.providers ?? [], configured: data.configured ?? {} }
}

/** How a model is named in the UI when there is nothing bound to name. */
export const UNBOUND_MODEL_LABEL = 'not configured'

/** The model a binding runs on, for the one-line "which model" summaries. */
export function bindingModelLabel(model: string | undefined) {
  return model?.trim() || UNBOUND_MODEL_LABEL
}

/**
 * Adapts a pickable row to what ProviderModelFields wants, which reads only
 * `id`, `sdk` and `alias`. The credential flags are true because the server
 * only returns configured providers here.
 */
export function asPickerProvider(item: OCRProviderInfo): AIProvider {
  return {
    id: item.id,
    sdk: item.sdk as ProviderSDK,
    alias: item.name,
    base_url: '',
    catalog: '',
    api_key_set: true,
    signed_in: true,
  }
}

export type OCRTestResult = {
  provider: string
  text: string
  char_count: number
  duration: string
}

export function testOCR(file: File, provider: string, model?: string) {
  const formData = new FormData()
  formData.append('file', file)
  formData.append('provider', provider)
  if (model) {
    formData.append('model', model)
  }

  return apiFetch<OCRTestResult>('/api/app/ocr/test', {
    method: 'POST',
    formData,
    fallbackError: 'OCR test failed',
  })
}
