import { apiFetch } from '../apiClient'

export const DEFAULT_APP_NAME = 'Lemmary'
export const DEFAULT_ACCENT = '#6e2620'

export type AppMeta = {
  appName: string
  accent: string
  /**
   * Whether the hosting provider owns AI configuration. On a managed instance
   * the Settings page has no Providers, Models or Duplicates sections — the
   * container's environment decides those, and the API refuses to change them.
   *
   * `undefined` means not known yet, or not answerable because the request
   * failed. Callers must treat that as "assume managed": defaulting it to
   * `false` renders the operator-owned sections during the first paint and,
   * when meta never arrives, leaves them rendered — and Save then sends fields
   * the server rejects, which fails the *whole* patch and takes the settings a
   * managed tenant really does own down with it.
   */
  aiManaged?: boolean
}

export async function getAppMeta(): Promise<AppMeta> {
  try {
    const data = await apiFetch<{ app_name?: string; accent?: string; ai_managed?: boolean }>(
      '/api/app/meta',
      {
        public: true,
        fallbackError: 'Failed to load app meta',
      },
    )
    const appName = typeof data.app_name === 'string' ? data.app_name.trim() : ''
    const accent = typeof data.accent === 'string' ? data.accent.trim() : ''
    return {
      appName: appName || DEFAULT_APP_NAME,
      accent: accent || DEFAULT_ACCENT,
      aiManaged: data.ai_managed === true,
    }
  } catch {
    // A name and an accent have safe defaults; who owns AI configuration does
    // not, so it is left unanswered rather than guessed at.
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
