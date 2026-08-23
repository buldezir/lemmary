import { apiFetch } from '../apiClient'

export const DEFAULT_APP_NAME = 'Paperless Go'
export const DEFAULT_ACCENT = '#6e2620'

export type AppMeta = {
  appName: string
  accent: string
}

export async function getAppMeta(): Promise<AppMeta> {
  try {
    const data = await apiFetch<{ app_name?: string; accent?: string }>('/api/app/meta', {
      public: true,
      fallbackError: 'Failed to load app meta',
    })
    const appName = typeof data.app_name === 'string' ? data.app_name.trim() : ''
    const accent = typeof data.accent === 'string' ? data.accent.trim() : ''
    return {
      appName: appName || DEFAULT_APP_NAME,
      accent: accent || DEFAULT_ACCENT,
    }
  } catch {
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
