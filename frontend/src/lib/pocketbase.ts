import PocketBase from 'pocketbase'

const pbUrl = import.meta.env.VITE_POCKETBASE_URL || document.location.origin

export const pb = new PocketBase(pbUrl)
export const pbAdminUrl = `${pbUrl}/_/`

export type DocumentTypeRecord = {
  id: string
  name: string
  name_original: string
}

export type CorrespondentRecord = {
  id: string
  name: string
  name_original: string
}

export type DocumentRecord = {
  id: string
  collectionId: string
  collectionName: string
  created: string
  updated: string
  file: string
  user: string
  title: string
  title_original: string
  purpose: string
  purpose_original: string
  document_date: string
  document_type: string
  correspondent: string
  ocr_text: string
  summary: string
  summary_original: string
  processing_status: 'pending' | 'processing' | 'completed' | 'failed' | 'needs_review'
  metadata_source: string
  confidence: number
  people_or_organizations: string[]
  tags: string[]
  checksum?: string
  text_fingerprint?: string
  duplicate_of?: string
  expand?: {
    tags?: TagRecord[]
    document_type?: DocumentTypeRecord
    correspondent?: CorrespondentRecord
    duplicate_of?: DocumentRecord
  }
}

export type TagRecord = {
  id: string
  name: string
}

export type ProcessingStep =
  | 'preview'
  | 'ocr'
  | 'detect_duplicates'
  | 'extract_metadata'
  | 'apply_metadata'

export type StepRunRecord = {
  name: ProcessingStep
  status: 'pending' | 'running' | 'completed' | 'failed' | 'skipped'
  attempts: number
  provider?: string
  model?: string
  prompt_version?: string
  started_at?: string
  finished_at?: string
  error?: string
}

export const FULL_PIPELINE_STEPS: ProcessingStep[] = [
  'preview',
  'ocr',
  'detect_duplicates',
  'extract_metadata',
  'apply_metadata',
]

export const EXTRACTION_PIPELINE_STEPS: ProcessingStep[] = ['extract_metadata', 'apply_metadata']

export const PROCESSING_STEP_LABELS: Record<ProcessingStep, string> = {
  preview: 'Preview',
  ocr: 'OCR',
  detect_duplicates: 'Detect duplicates',
  extract_metadata: 'Extract metadata',
  apply_metadata: 'Apply metadata',
}

export const PROCESSING_STEP_DESCRIPTIONS: Record<ProcessingStep, string> = {
  preview: 'Regenerate the first-page preview image (PDF only)',
  ocr: 'Re-run text extraction on the original file',
  detect_duplicates: 'Compare OCR text for near-duplicates (when enabled in Settings)',
  extract_metadata: 'Re-run AI metadata extraction from OCR text',
  apply_metadata: 'Write extracted metadata onto the document',
}

export function orderedProcessingSteps(selected: Iterable<ProcessingStep>): ProcessingStep[] {
  const chosen = new Set(selected)
  return FULL_PIPELINE_STEPS.filter((step) => chosen.has(step))
}

export function forceStepsForReprocess(steps: ProcessingStep[]): ProcessingStep[] {
  return steps.filter((step) => step !== 'apply_metadata')
}

export function defaultReprocessSteps(hasOcrText: boolean): ProcessingStep[] {
  return hasOcrText ? [...EXTRACTION_PIPELINE_STEPS] : [...FULL_PIPELINE_STEPS]
}

export type ProcessingJobRecord = {
  id: string
  document: string
  status: string
  steps: ProcessingStep[]
  step_runs?: StepRunRecord[]
  current_step?: string
  started_at: string
  finished_at: string
  created: string
  updated: string
}

export type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
}

export type OCRProviderInfo = {
  id: string
  name: string
  sdk: string
}

export type OCRTestResult = {
  provider: string
  text: string
  char_count: number
  duration: string
}

export function fileUrl(record: DocumentRecord, filename?: string) {
  return pb.files.getURL(record, filename ?? record.file)
}

export async function reprocessDocument(
  documentId: string,
  steps: ProcessingStep[],
  forceSteps?: ProcessingStep[],
) {
  await ensureAuth()
  await pb.collection('documents').update(documentId, {
    processing_status: 'pending',
  })
  return pb.collection('processing_jobs').create({
    document: documentId,
    status: 'pending',
    steps,
    ...(forceSteps?.length ? { force_steps: forceSteps } : {}),
  })
}

export async function chatWithDocument(documentId: string, messages: ChatMessage[]) {
  await ensureAuth()

  const response = await fetch(`${pbUrl}/api/app/documents/${documentId}/chat`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: pb.authStore.token,
    },
    body: JSON.stringify({ messages }),
  })

  const data = (await response.json()) as { message?: ChatMessage; detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to get AI response')
  }
  if (!data.message) {
    throw new Error('AI response was empty')
  }
  return data.message
}

export type SearchDocumentHit = {
  id: string
  title: string
  document_date?: string
  summary?: string
  ocr_snippet?: string
  document_type?: string
  correspondent?: string
  tags?: string[]
}

export type SearchMode = 'shallow' | 'deep'

export async function deepSearch(messages: ChatMessage[], mode: SearchMode = 'shallow') {
  await ensureAuth()

  const response = await fetch(`${pbUrl}/api/app/search`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: pb.authStore.token,
    },
    body: JSON.stringify({ messages, mode }),
  })

  const data = (await response.json()) as {
    message?: ChatMessage
    documents?: SearchDocumentHit[]
    detail?: string
  }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to run deep search')
  }
  if (!data.message) {
    throw new Error('AI response was empty')
  }
  return {
    message: data.message,
    documents: data.documents ?? [],
  }
}

export async function listOCRProviders() {
  await ensureAuth()

  const response = await fetch(`${pbUrl}/api/app/ocr/providers`, {
    headers: {
      Authorization: pb.authStore.token,
    },
  })

  const data = (await response.json()) as { providers?: OCRProviderInfo[]; detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to load OCR providers')
  }
  return data.providers ?? []
}

export async function testOCR(file: File, provider: string, model?: string) {
  await ensureAuth()

  const formData = new FormData()
  formData.append('file', file)
  formData.append('provider', provider)
  if (model) formData.append('model', model)

  const response = await fetch(`${pbUrl}/api/app/ocr/test`, {
    method: 'POST',
    headers: {
      Authorization: pb.authStore.token,
    },
    body: formData,
  })

  const data = (await response.json()) as OCRTestResult & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'OCR test failed')
  }
  return data
}

export class AuthRequiredError extends Error {
  constructor() {
    super('Authentication required')
    this.name = 'AuthRequiredError'
  }
}

export function hasDevCredentials() {
  const email = import.meta.env.VITE_DEV_USER_EMAIL
  const password = import.meta.env.VITE_DEV_USER_PASSWORD
  return Boolean(email && password)
}

export async function loginWithPassword(email: string, password: string) {
  clearMeCache()
  try {
    await pb.collection('users').authWithPassword(email, password)
    return
  } catch {
    // Fall through to superuser (legacy installs / PocketBase admin accounts).
  }

  await pb.collection('_superusers').authWithPassword(email, password)
  const response = await fetch(`${pbUrl}/api/app/ensure-user`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: pb.authStore.token,
    },
    body: JSON.stringify({ password }),
  })
  const data = (await response.json()) as { detail?: string }
  if (!response.ok) {
    pb.authStore.clear()
    throw new Error(data.detail ?? 'Failed to create paired user account')
  }
  // App sessions must be users-collection so documents.user relations validate.
  await pb.collection('users').authWithPassword(email, password)
}

export type MeInfo = {
  email: string
  is_admin: boolean
}

let meCache: MeInfo | null = null

export function clearMeCache() {
  meCache = null
}

pb.authStore.onChange(() => {
  meCache = null
})

export async function getMe(): Promise<MeInfo> {
  await ensureAuth()
  if (meCache) {
    return meCache
  }

  const response = await fetch(`${pbUrl}/api/app/me`, {
    headers: {
      Authorization: pb.authStore.token,
    },
  })
  const data = (await response.json()) as MeInfo & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to load account info')
  }
  meCache = {
    email: typeof data.email === 'string' ? data.email : '',
    is_admin: Boolean(data.is_admin),
  }
  return meCache
}

/** True when the current users session is a paired admin (or rare superuser JWT). */
export async function isAdmin() {
  if (!pb.authStore.isValid) {
    return false
  }
  try {
    const me = await getMe()
    return me.is_admin
  } catch {
    return false
  }
}

export function logout() {
  clearMeCache()
  pb.authStore.clear()
}

export function getUserDisplayName() {
  const record = pb.authStore.record
  if (!record) {
    return ''
  }

  const name = typeof record.name === 'string' ? record.name.trim() : ''
  const email = typeof record.email === 'string' ? record.email.trim() : ''
  return name || email
}

export const DEFAULT_APP_NAME = 'Paperless Go'
export const DEFAULT_ACCENT = '#111827'

export type AppMeta = {
  appName: string
  accent: string
}

export async function getAppMeta(): Promise<AppMeta> {
  try {
    const response = await fetch(`${pbUrl}/api/app/meta`)
    const data = (await response.json()) as { app_name?: string; accent?: string; detail?: string }
    if (response.ok) {
      const appName = typeof data.app_name === 'string' ? data.app_name.trim() : ''
      const accent = typeof data.accent === 'string' ? data.accent.trim() : ''
      return {
        appName: appName || DEFAULT_APP_NAME,
        accent: accent || DEFAULT_ACCENT,
      }
    }
  } catch {
    // Fall through to defaults.
  }
  return { appName: DEFAULT_APP_NAME, accent: DEFAULT_ACCENT }
}

export type SetupStatus = {
  needs_admin: boolean
  needs_config: boolean
  has_ocr: boolean
  has_llm: boolean
  provider_count: number
}

export async function getSetupStatus(): Promise<SetupStatus> {
  const response = await fetch(`${pbUrl}/api/app/setup/status`)
  const data = (await response.json()) as SetupStatus & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to load setup status')
  }
  return data
}

export async function createSetupAdmin(email: string, password: string, passwordConfirm: string) {
  const response = await fetch(`${pbUrl}/api/app/setup/admin`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password, passwordConfirm }),
  })
  const data = (await response.json()) as { email?: string; id?: string; detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to create admin account')
  }
  return data
}

/** Pick black or white text for contrast on an accent background. */
export function accentContrastText(accent: string): string {
  const hex = accent.trim().replace(/^#/, '')
  if (!/^[0-9a-fA-F]{6}$/.test(hex)) {
    return '#ffffff'
  }
  const r = Number.parseInt(hex.slice(0, 2), 16)
  const g = Number.parseInt(hex.slice(2, 4), 16)
  const b = Number.parseInt(hex.slice(4, 6), 16)
  const luma = (0.299 * r + 0.587 * g + 0.114 * b) / 255
  return luma > 0.6 ? '#000000' : '#ffffff'
}

export type ProviderSDK = 'openai' | 'openrouter' | 'google_vision' | 'mistral'

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
}

export async function listAIProviders() {
  await ensureAuth()
  const response = await fetch(`${pbUrl}/api/app/providers`, {
    headers: { Authorization: pb.authStore.token },
  })
  const data = (await response.json()) as { providers?: AIProvider[]; detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to load providers')
  }
  return data.providers ?? []
}

export async function createAIProvider(body: AIProviderWrite) {
  await ensureAuth()
  const response = await fetch(`${pbUrl}/api/app/providers`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: pb.authStore.token,
    },
    body: JSON.stringify(body),
  })
  const data = (await response.json()) as AIProvider & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to create provider')
  }
  return data as AIProvider
}

export async function updateAIProvider(id: string, body: Partial<AIProviderWrite>) {
  await ensureAuth()
  const response = await fetch(`${pbUrl}/api/app/providers/${id}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      Authorization: pb.authStore.token,
    },
    body: JSON.stringify(body),
  })
  const data = (await response.json()) as AIProvider & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to update provider')
  }
  return data as AIProvider
}

export async function deleteAIProvider(id: string) {
  await ensureAuth()
  const response = await fetch(`${pbUrl}/api/app/providers/${id}`, {
    method: 'DELETE',
    headers: { Authorization: pb.authStore.token },
  })
  const data = (await response.json()) as { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to delete provider')
  }
}

export async function listProviderModels(id: string, purpose: 'ocr' | 'llm' = 'llm') {
  await ensureAuth()
  const response = await fetch(`${pbUrl}/api/app/providers/${id}/models?for=${purpose}`, {
    headers: { Authorization: pb.authStore.token },
  })
  const data = (await response.json()) as { models?: CatalogModel[]; sdk?: string; detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to load models')
  }
  return { models: data.models ?? [], sdk: data.sdk ?? '' }
}

export type AppSettings = {
  ocr_provider_id: string
  ocr_model: string
  extract_provider_id: string
  extract_model: string
  chat_provider_id: string
  chat_model: string
  search_provider_id: string
  search_model: string
  ocr_timeout_sec: number
  processing_result_language: string
  deep_search_languages: string
  openai_timeout_sec: number
  worker_timeout_sec: number
  worker_max_retries: number
  extraction_prompt_version: string
  near_duplicate_detection_enabled: boolean
  near_duplicate_threshold: number
}

export type AppSettingsPatch = {
  ocr_provider_id?: string
  ocr_model?: string
  extract_provider_id?: string
  extract_model?: string
  chat_provider_id?: string
  chat_model?: string
  search_provider_id?: string
  search_model?: string
  ocr_timeout_sec?: number
  processing_result_language?: string
  deep_search_languages?: string
  openai_timeout_sec?: number
  worker_timeout_sec?: number
  worker_max_retries?: number
  extraction_prompt_version?: string
  near_duplicate_detection_enabled?: boolean
  near_duplicate_threshold?: number
}

export async function getAppSettings() {
  await ensureAuth()

  const response = await fetch(`${pbUrl}/api/app/settings`, {
    headers: {
      Authorization: pb.authStore.token,
    },
  })

  const data = (await response.json()) as AppSettings & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to load settings')
  }
  return data as AppSettings
}

export async function updateAppSettings(patch: AppSettingsPatch) {
  await ensureAuth()

  const response = await fetch(`${pbUrl}/api/app/settings`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      Authorization: pb.authStore.token,
    },
    body: JSON.stringify(patch),
  })

  const data = (await response.json()) as AppSettings & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to save settings')
  }
  return data as AppSettings
}

export type DuplicateScanResult = {
  scanned: number
  checksum_backfilled: number
  exact_marked: number
  near_marked: number
  fingerprints_filled: number
}

export async function scanDuplicates() {
  await ensureAuth()

  const response = await fetch(`${pbUrl}/api/app/duplicates/scan`, {
    method: 'POST',
    headers: {
      Authorization: pb.authStore.token,
    },
  })

  const data = (await response.json()) as DuplicateScanResult & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Duplicate scan failed')
  }
  return data as DuplicateScanResult
}

export type NgxImportMode = 'preserve' | 'reprocess'

export type NgxImportResult = {
  imported: number
  skipped_duplicates: number
  failed: number
  tags_upserted: number
  correspondents_upserted: number
  document_types_upserted: number
  errors: string[]
}

type NgxImportJobStart = {
  job_id: string
  status: string
  detail?: string
}

type NgxImportJobStatus = {
  job_id: string
  status: 'running' | 'completed' | 'failed' | string
  error?: string
  result?: NgxImportResult
  detail?: string
}

const importPollIntervalMs = 500
const importPollMaxAttempts = 600

export async function importFromNgx(url: string, apiKey: string, mode: NgxImportMode = 'preserve') {
  await ensureAuth()

  const startResponse = await fetch(`${pbUrl}/api/app/import/ngx`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: pb.authStore.token,
    },
    body: JSON.stringify({ url, api_key: apiKey, mode }),
  })

  const startData = (await startResponse.json()) as NgxImportJobStart
  if (!startResponse.ok) {
    throw new Error(startData.detail ?? 'Import failed to start')
  }
  if (!startData.job_id) {
    throw new Error('Import job id missing from server response')
  }

  for (let attempt = 0; attempt < importPollMaxAttempts; attempt++) {
    const statusResponse = await fetch(
      `${pbUrl}/api/app/import/ngx/status?job_id=${encodeURIComponent(startData.job_id)}`,
      {
        headers: {
          Authorization: pb.authStore.token,
        },
      },
    )
    const statusData = (await statusResponse.json()) as NgxImportJobStatus
    if (!statusResponse.ok) {
      throw new Error(statusData.detail ?? 'Failed to poll import status')
    }
    if (statusData.status === 'completed') {
      const result = statusData.result
      if (!result) {
        throw new Error('Import completed without a result')
      }
      return {
        ...result,
        errors: result.errors ?? [],
      } as NgxImportResult
    }
    if (statusData.status === 'failed') {
      throw new Error(statusData.error ?? 'Import failed')
    }
    await new Promise((resolve) => setTimeout(resolve, importPollIntervalMs))
  }

  throw new Error('Import timed out while waiting for completion')
}

export type ExportArchiveMode = 'originals' | 'ocr' | 'metadata'

export async function downloadDocumentsArchive(mode: ExportArchiveMode = 'originals') {
  await ensureAuth()

  const response = await fetch(
    `${pbUrl}/api/app/documents/export?mode=${encodeURIComponent(mode)}`,
    {
      headers: {
        Authorization: pb.authStore.token,
      },
    },
  )

  if (!response.ok) {
    let detail = 'Failed to download archive'
    try {
      const data = (await response.json()) as { detail?: string }
      if (data.detail) detail = data.detail
    } catch {
      // response may be non-JSON on some errors
    }
    throw new Error(detail)
  }

  const blob = await response.blob()
  const objectUrl = URL.createObjectURL(blob)
  try {
    const anchor = document.createElement('a')
    anchor.href = objectUrl
    anchor.download = 'paperless-export.zip'
    anchor.rel = 'noopener'
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
  } finally {
    URL.revokeObjectURL(objectUrl)
  }
}

export function parseDuplicateOfId(message: string): string | null {
  const match = message.match(/duplicate of ([a-z0-9]{15})/i)
  return match?.[1] ?? null
}

export async function ensureAuth() {
  if (pb.authStore.isValid) {
    return
  }

  const email = import.meta.env.VITE_DEV_USER_EMAIL
  const password = import.meta.env.VITE_DEV_USER_PASSWORD

  if (!email || !password) {
    throw new AuthRequiredError()
  }

  try {
    await pb.collection('users').authWithPassword(email, password)
  } catch {
    throw new AuthRequiredError()
  }
}
