import { apiFetch } from '../apiClient'

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
  /**
   * The search model's context window, in tokens. Research mode reads
   * documents until this budget is spent, so it is the only thing bounding a
   * run — too high and requests overflow the model, too low and the answer is
   * drawn from fewer documents than it should be.
   */
  search_context_tokens: number
  openai_timeout_sec: number
  worker_timeout_sec: number
  worker_max_retries: number
  /**
   * Bookkeeping only: recorded on each document's extraction step so its
   * metadata can be traced to a prompt. Not editable in the UI — it is set by
   * EXTRACTION_PROMPT_VERSION or through the API.
   */
  extraction_prompt_version: string
  near_duplicate_detection_enabled: boolean
  near_duplicate_threshold: number
}

export type AppSettingsPatch = Partial<AppSettings>

export function getAppSettings() {
  return apiFetch<AppSettings>('/api/app/settings', {
    fallbackError: 'Failed to load settings',
  })
}

export function updateAppSettings(patch: AppSettingsPatch) {
  return apiFetch<AppSettings>('/api/app/settings', {
    method: 'PATCH',
    body: patch,
    fallbackError: 'Failed to save settings',
  })
}
