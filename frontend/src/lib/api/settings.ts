import { apiFetch } from '../apiClient'
import { setAlwaysRequireReview } from '../reviewPolicy'
import { invalidateAppMeta } from './meta'

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
  /**
   * The admin's own additions to the extraction prompt: house conventions for
   * titles, types, correspondents and tags. Appended to the built-in prompt,
   * which stays in force — these cannot change which fields are stored. Empty
   * means the prompt is what it always was. Tenant-owned, so a managed
   * instance keeps it.
   */
  extraction_rules: string
  /** Tenant-owned, so a managed instance keeps it. */
  always_require_review: boolean
  near_duplicate_detection_enabled: boolean
  near_duplicate_threshold: number
  /** Instance name, shown in the header and stamped on emails and passkeys. */
  app_name: string
  /** Accent color as #rrggbb. Empty means the built-in accent. */
  accent: string
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

export async function updateAppSettings(patch: AppSettingsPatch) {
  const settings = await apiFetch<AppSettings>('/api/app/settings', {
    method: 'PATCH',
    body: patch,
    fallbackError: 'Failed to save settings',
  })
  // Normally learned once from /api/app/meta at boot; taking it from the save
  // response spares an admin who just turned it on a page reload.
  setAlwaysRequireReview(settings.always_require_review)
  invalidateAppMeta()
  return settings
}
