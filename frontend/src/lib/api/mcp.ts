import { apiFetch } from '../apiClient'

export type MCPStatus = {
  enabled: boolean
  /** Absolute endpoint URL, empty while disabled. */
  url: string
}

export async function getMCPStatus(): Promise<MCPStatus> {
  const data = await apiFetch<{ enabled?: boolean; path?: string }>('/api/app/mcp', {
    fallbackError: 'Failed to check the MCP endpoint',
  })
  if (data.enabled !== true) {
    return { enabled: false, url: '' }
  }
  return { enabled: true, url: new URL(data.path || '/api/mcp', window.location.origin).toString() }
}

export async function createMCPToken(): Promise<string> {
  const data = await apiFetch<{ token: string }>('/api/app/mcp/token', {
    method: 'POST',
    body: {},
    fallbackError: 'Failed to create a token',
  })
  return data.token
}
