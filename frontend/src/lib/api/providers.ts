import { apiFetch } from '../apiClient'

export type ProviderSDK =
  | 'openai'
  | 'openrouter'
  | 'google_vision'
  | 'mistral'
  | 'docling'
  | 'paddleocr'

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

export type CatalogModel = {
  id: string
  name: string
  /** Context length in tokens, when the provider reports one (OpenAI does not). */
  context_window?: number
}

/**
 * Must stay identical to aiprovider.DefaultBaseURL in backend/internal/aiprovider/sdk.go.
 * The two local entries are the service names from docker-compose.local-ocr.yml,
 * so a provider added from the wizard needs nothing typed.
 */
export const SDK_DEFAULT_BASE: Record<ProviderSDK, string> = {
  openai: 'https://api.openai.com/v1',
  openrouter: 'https://openrouter.ai/api/v1',
  mistral: 'https://api.mistral.ai/v1',
  google_vision: '',
  docling: 'http://docling:5001',
  paddleocr: 'http://paddleocr:8080',
}

export const SDK_OPTIONS: { value: ProviderSDK; label: string }[] = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'mistral', label: 'Mistral' },
  { value: 'google_vision', label: 'Google Cloud Vision' },
  { value: 'docling', label: 'Docling (local)' },
  { value: 'paddleocr', label: 'PaddleOCR (local)' },
]

export function sdkLabel(sdk: ProviderSDK | string) {
  return SDK_OPTIONS.find((option) => option.value === sdk)?.label ?? sdk
}

export function isLLMProvider(sdk: string) {
  return sdk === 'openai' || sdk === 'openrouter' || sdk === 'mistral'
}

/**
 * Mirrors aiprovider.RequiresAPIKey. The local OCR sidecars run on the
 * operator's own host and are reached by address alone; everything else needs a
 * credential. Default-true like the Go side, so an unknown SDK still asks.
 */
export function requiresAPIKey(sdk?: string) {
  return sdk !== 'docling' && sdk !== 'paddleocr'
}

/**
 * Mirrors aiprovider.RequiresOCRModel: the SDKs that read a document without
 * being told a model. Google Vision has none to give; the sidecars each serve
 * one pipeline, and what looks like a model there is an optional engine name.
 */
export function usesOCRModel(sdk?: string) {
  return sdk !== 'google_vision' && sdk !== 'docling' && sdk !== 'paddleocr'
}

/**
 * What the OCR "model" means for a local sidecar, shown under the free-text box
 * the picker falls back to when the provider has no catalogue. Empty for every
 * other SDK, where the field really does name a model.
 */
export function localOCRModelHint(sdk?: string) {
  switch (sdk) {
    case 'docling':
      return 'Optional. Names Docling\u2019s OCR engine — rapidocr, easyocr, tesserocr or tesseract. Leave blank to use the container\u2019s default.'
    case 'paddleocr':
      return 'Optional. Names the served pipeline — pp-structurev3 (default, markdown with tables) or ocr (faster, plain lines).'
    default:
      return ''
  }
}

/** The hint shown under a local provider's Base URL, in place of an API key. */
export const LOCAL_OCR_HINT =
  'Runs on your own host, so no API key is needed — the address is the whole configuration. The default is the service name from docker-compose.local-ocr.yml.'

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

export type ModelPurpose = 'ocr' | 'llm' | 'embedding'

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
