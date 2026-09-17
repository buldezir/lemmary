import { apiFetch } from '../apiClient'
import { setAlwaysRequireReview } from '../reviewPolicy'

export const DEFAULT_APP_NAME = 'Lemmary'
export const DEFAULT_ACCENT = '#6e2620'

export type AppMeta = {
  appName: string
  accent: string
  /**
   * Hosting provider owns AI configuration. `undefined` is in-flight or failed:
   * treat as managed. Defaulting to false flashes those sections, and Save
   * would send fields the server rejects, failing the whole patch.
   */
  aiManaged?: boolean
  /**
   * Whether every AI-extracted document waits in the review Inbox. Unknown
   * reads as off, which is the behaviour before the flag existed.
   */
  alwaysRequireReview?: boolean
  /**
   * Whether a web-search provider is bound, which is what lets a chat offer the
   * web toggle at all. Unknown reads as off: a toggle that cannot work is a dead
   * end, and the tools are metered.
   */
  webSearch?: boolean
}

// One request per page load, shared by three components and the auth gate --
// which has to have the answer before the first route renders, because
// always_require_review decides what a documents-list URL means.
let pending: Promise<AppMeta> | null = null

export function getAppMeta(): Promise<AppMeta> {
  pending ??= fetchAppMeta()
  return pending
}

type Listener = () => void

const listeners = new Set<Listener>()

/**
 * Forgetting the cache is not enough on its own: nothing re-reads it, so the
 * nav kept offering yesterday's answer until a reload. Same shape as
 * lib/documentEvents.ts.
 */
export function onAppMetaChanged(listener: Listener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** Forgets the cached meta, so the next read sees a just-saved setting. */
export function invalidateAppMeta(): void {
  pending = null
  // Copied first: a listener that unsubscribes itself while we notify must not
  // shorten the set we are walking.
  for (const listener of [...listeners]) {
    listener()
  }
}

async function fetchAppMeta(): Promise<AppMeta> {
  try {
    const data = await apiFetch<{
      app_name?: string
      accent?: string
      ai_managed?: boolean
      always_require_review?: boolean
      web_search?: boolean
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
      alwaysRequireReview: data.always_require_review === true,
      webSearch: data.web_search === true,
    }
  } catch {
    // A name and accent have safe defaults; who owns AI configuration does not.
    // Not cached, so the next caller retries.
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
