import { t } from '../../i18n'
import { apiFetch } from '../apiClient'

/** Each opens only its own endpoint: MCP for agents, the Paperless-ngx API for apps. */
export type TokenScope = 'mcp' | 'paperless'

/** The token itself is shown once, when created, and never listed. */
export type APIToken = {
  id: string
  name: string
  created: string
  last_used: string
}

export async function listTokens(scope: TokenScope): Promise<APIToken[]> {
  const data = await apiFetch<{ tokens?: APIToken[] }>(`/api/app/tokens?scope=${scope}`, {
    fallbackError: t('tokens.loadFailed'),
  })
  return data.tokens ?? []
}

export async function createToken(scope: TokenScope, name = ''): Promise<string> {
  const data = await apiFetch<{ token: string }>('/api/app/tokens', {
    method: 'POST',
    body: { scope, name },
    fallbackError: t('account.tokenFailed'),
  })
  return data.token
}

export function revokeToken(id: string) {
  return apiFetch<unknown>(`/api/app/tokens/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    fallbackError: t('tokens.revokeFailed'),
  })
}
