import { apiFetch } from '../apiClient'
import { setAlwaysRequireReview } from '../reviewPolicy'

export const DEFAULT_APP_NAME = 'Lemmary'
export const DEFAULT_ACCENT = '#6e2620'

export type AppMeta = {
  appName: string
  accent: string
  /**
   * Hosting provider owns AI configuration. `undefined` is in-flight or a
   * failed request: treat as managed. Defaulting to false flashes those
   * sections and, if meta never arrives, Save sends fields the server
   * rejects, which fails the whole patch including tenant-owned timeouts.
   */
  aiManaged?: boolean
  /**
   * Whether this instance allows signing in with a ChatGPT subscription
   * (AI_CHATGPT_LOGIN). Unknown reads as off, the opposite default to
   * aiManaged and for the same reason: both err towards offering less. An SDK
   * shown here that the server refuses is a dead end an admin cannot diagnose.
   */
  chatgptLogin?: boolean
  /**
   * Whether every AI-extracted document waits in the review Inbox. Unknown
   * reads as off, like chatgptLogin: off is the behaviour before the Inbox
   * existed, and guessing "on" would narrow a stranger's document list.
   */
  alwaysRequireReview?: boolean
}

// One request per page load, shared by every caller.
//
// Meta is per-instance and near-constant, but three components ask for it and
// so does the auth gate -- and the gate has to have the answer before the first
// route renders, because always_require_review decides what a documents-list
// URL means. Caching the promise is what makes that a shared await instead of a
// fourth request. invalidateAppMeta() drops it when Settings is saved.
let pending: Promise<AppMeta> | null = null

export function getAppMeta(): Promise<AppMeta> {
  pending ??= fetchAppMeta()
  return pending
}

/** Forgets the cached meta, so the next read sees a just-saved setting. */
export function invalidateAppMeta(): void {
  pending = null
}

async function fetchAppMeta(): Promise<AppMeta> {
  try {
    const data = await apiFetch<{
      app_name?: string
      accent?: string
      ai_managed?: boolean
      chatgpt_login?: boolean
      always_require_review?: boolean
    }>('/api/app/meta', {
      public: true,
      fallbackError: 'Failed to load app meta',
    })
    const appName = typeof data.app_name === 'string' ? data.app_name.trim() : ''
    const accent = typeof data.accent === 'string' ? data.accent.trim() : ''
    setAlwaysRequireReview(data.always_require_review === true)
    return {
      appName: appName || DEFAULT_APP_NAME,
      accent: accent || DEFAULT_ACCENT,
      aiManaged: data.ai_managed === true,
      chatgptLogin: data.chatgpt_login === true,
      alwaysRequireReview: data.always_require_review === true,
    }
  } catch {
    // A name and accent have safe defaults; who owns AI configuration does not.
    // A failed request is also not cached: the next caller retries.
    pending = null
    return { appName: DEFAULT_APP_NAME, accent: DEFAULT_ACCENT }
  }
}

export type SetupStatus = {
  needs_admin: boolean
  needs_config: boolean
  has_ocr: boolean
  has_llm: boolean
  provider_count: number
}

export function getSetupStatus() {
  return apiFetch<SetupStatus>('/api/app/setup/status', {
    public: true,
    fallbackError: 'Failed to load setup status',
  })
}

export function createSetupAdmin(email: string, password: string, passwordConfirm: string) {
  return apiFetch<{ email?: string; id?: string }>('/api/app/setup/admin', {
    method: 'POST',
    public: true,
    body: { email, password, passwordConfirm },
    fallbackError: 'Failed to create admin account',
  })
}
