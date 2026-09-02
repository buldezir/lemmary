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
  /**
   * The Deep Search helper binding: the cheaper model that distils long reads
   * into notes and surveys many documents for one question. Empty means the
   * search model does that work itself.
   */
  search_helper_provider_id: string
  search_helper_model: string
  /**
   * The retrieval embedding binding. Empty means Deep Search runs on keywords
   * alone, which is what every install did before this existed.
   */
  embedding_provider_id: string
  embedding_model: string
  /**
   * Vector length, reported by the provider on the first real request. Read-only:
   * only the provider knows it, and a number typed next to a model that
   * disagrees would build an index that silently drops every vector.
   */
  embedding_dims: number
  ocr_timeout_sec: number
  processing_result_language: string
  deep_search_languages: string
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

export type AppSettingsPatch = Partial<Omit<AppSettings, 'embedding_dims'>>

/** How much of the archive has been embedded. See GET /api/app/settings/embeddings. */
export type EmbeddingStats = {
  enabled: boolean
  model: string
  dims: number
  /** Documents that can be embedded at all: not duplicates, with text, not mid-pipeline. */
  total: number
  embedded: number
  stale: number
  failed: number
  /** Documents the backfill still has to get to. */
  pending: number
  chunks: number
}

export function getEmbeddingStats() {
  return apiFetch<EmbeddingStats>('/api/app/settings/embeddings', {
    fallbackError: 'Failed to load embedding statistics',
  })
}

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
