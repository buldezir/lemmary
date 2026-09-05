import { apiFetch } from '../apiClient'

export type ProviderSDK =
  | 'openai'
  | 'openrouter'
  | 'google_vision'
  | 'mistral'
  | 'local'
  | 'docling'

export type AIProvider = {
  id: string
  sdk: ProviderSDK
  alias: string
  base_url: string
  api_key_set: boolean
}

export type AIProviderWrite = {
  sdk: ProviderSDK
  alias: string
  base_url?: string
  api_key?: string
}

export type ModelPurpose = 'ocr' | 'llm' | 'embedding'

export type CatalogModel = {
  id: string
  name: string
  /** Context length in tokens, when the provider reports one (OpenAI does not). */
  context_window?: number
}

/**
 * Must stay identical to aiprovider.DefaultBaseURL in backend/internal/aiprovider/sdk.go.
 * The two sidecar entries are the service names from the compose overlays, so a
 * provider added from the wizard needs nothing typed.
 */
export const SDK_DEFAULT_BASE: Record<ProviderSDK, string> = {
  openai: 'https://api.openai.com/v1',
  openrouter: 'https://openrouter.ai/api/v1',
  mistral: 'https://api.mistral.ai/v1',
  google_vision: '',
  // The service names in docker-compose.embeddings.yml and
  // docker-compose.local-ocr.yml.
  local: 'http://embeddings:80/v1',
  docling: 'http://docling:5001',
}

export const SDK_OPTIONS: { value: ProviderSDK; label: string }[] = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'mistral', label: 'Mistral' },
  { value: 'google_vision', label: 'Google Cloud Vision' },
  { value: 'local', label: 'Local Embeddings (huggingface/text-embeddings-inference)' },
  { value: 'docling', label: 'Local OCR (Docling)' },
]

export function sdkLabel(sdk: ProviderSDK | string) {
  return SDK_OPTIONS.find((option) => option.value === sdk)?.label ?? sdk
}

/**
 * The alias a provider gets when the field is left blank, and what the field
 * suggests. Mirrors aiprovider.DefaultAlias.
 *
 * Deliberately not sdkLabel, whose job is to tell two dropdown rows apart and
 * which names the upstream project to do it. Stored as an alias that reads back
 * through providerOptionLabel as `alias (sdk label)`, it nests its own
 * parentheses inside the label's.
 */
export function sdkAliasDefault(sdk: ProviderSDK | string) {
  if (sdk === 'local') return 'Local embeddings'
  if (sdk === 'docling') return 'Docling'
  return sdkLabel(sdk)
}

export function isLLMProvider(sdk: string) {
  return sdk === 'openai' || sdk === 'openrouter' || sdk === 'mistral'
}

/**
 * Whether an SDK can serve the embedding binding. Deliberately not
 * isLLMProvider, which it used to coincide with: `local` embeds without
 * chatting, and google_vision and docling do neither. Mirrors
 * aiprovider.CanEmbed.
 */
export function canEmbedProvider(sdk: string) {
  return isLLMProvider(sdk) || sdk === 'local'
}

/**
 * Mirrors aiprovider.RequiresAPIKey. Only the two sidecars are exempt: they run
 * on the operator's own host and are reached by address alone. Default-true
 * like the Go side, so an unknown SDK still asks.
 */
export function requiresAPIKey(sdk?: string) {
  return sdk !== 'local' && sdk !== 'docling'
}

/** Which SDKs may be bound to a given task, for the provider pickers. */
export function providerServesPurpose(sdk: string, purpose: ModelPurpose) {
  if (purpose === 'embedding') return canEmbedProvider(sdk)
  if (purpose === 'llm') return isLLMProvider(sdk)
  // OCR is the binding google_vision and docling exist for, and the one a
  // local embeddings endpoint cannot serve. Mirrors aiprovider.CanOCR.
  return sdk !== 'local'
}

/**
 * The providers a binding may be offered, which is `providerServesPurpose` plus
 * the one already bound -- kept whatever its SDK, so an existing binding never
 * renders as blank.
 *
 * It exists so no call site pre-filters its own list. Settings passed the
 * embedding picker a list already narrowed to `isLLMProvider`, which stripped
 * every `local` provider before the purpose filter could see it: the SDK that
 * embeds without chatting was the one binding it could not reach.
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
 * Mirrors aiprovider.RequiresOCRModel: the SDKs that read a document without
 * being told a model. Google Vision has none to give; for the sidecar, what
 * looks like a model is an optional OCR engine name.
 */
export function usesOCRModel(sdk?: string) {
  return sdk !== 'google_vision' && sdk !== 'docling'
}

/**
 * What the OCR "model" means for the local sidecar, shown under the free-text
 * box the picker falls back to when the provider has no catalogue. Empty for
 * every other SDK, where the field really does name a model.
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
  if (requiresAPIKey(sdk)) return ''
  const overlay = sdk === 'local' ? 'docker-compose.embeddings.yml' : 'docker-compose.local-ocr.yml'
  return `Runs on your own host, so no API key is needed \u2014 the address is the whole configuration. The default is the service name from ${overlay}.`
}

/**
 * The guide behind a keyless provider's hint. Neither sidecar answers until its
 * compose overlay is up, and that is the one thing the hint cannot fit; the
 * link is how an operator gets from the dropdown to the instructions. Null for
 * the hosted SDKs, which have a key field there instead.
 *
 * The `.html` is load-bearing. VitePress has no cleanUrls, so the built pages
 * are files, and the static handler in appwire/wire.go never tries an .html
 * suffix -- a bare /docs/local_ocr falls through to the SPA.
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

export function providerOptionLabel(item: Pick<AIProvider, 'alias' | 'sdk'>) {
  const sdk = sdkLabel(item.sdk)
  return item.alias === sdk ? item.alias : `${item.alias} (${sdk})`
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
