import { apiFetch } from '../apiClient'

export const DEFAULT_APP_NAME = 'Lemmary'
export const DEFAULT_ACCENT = '#6e2620'

export type AppMeta = {
  appName: string
  accent: string
  /**
   * Whether the login screen should offer passkey sign-in. The server answers
   * both halves of that question: the address can carry a ceremony at all, and
   * at least one passkey exists to offer. Defaults to false so a server that
   * predates the flag simply does not show the button.
   */
  passkeys: boolean
}

export async function getAppMeta(): Promise<AppMeta> {
  try {
    const data = await apiFetch<{ app_name?: string; accent?: string; passkeys?: boolean }>(
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
      passkeys: data.passkeys === true,
    }
  } catch {
    return { appName: DEFAULT_APP_NAME, accent: DEFAULT_ACCENT, passkeys: false }
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
