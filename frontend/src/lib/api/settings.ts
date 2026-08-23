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
  openai_timeout_sec: number
  worker_timeout_sec: number
  worker_max_retries: number
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
