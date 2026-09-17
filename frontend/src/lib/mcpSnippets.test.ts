import { describe, expect, it } from 'vitest'
import { mcpSnippets } from './mcpSnippets'

const url = 'https://docs.example.com/api/mcp'
const token = 'tok.en'

describe('mcpSnippets', () => {
  it('puts the endpoint and the bearer token into every snippet', () => {
    const snippets = mcpSnippets(url, token)
    expect(snippets.length).toBeGreaterThanOrEqual(8)
    for (const snippet of snippets) {
      expect(snippet.text, snippet.id).toContain(url)
      expect(snippet.text, snippet.id).toContain(token)
      expect(snippet.name).not.toBe('')
      expect(snippet.where).not.toBe('')
    }
  })

  it('emits valid JSON where the agent reads a JSON file', () => {
    for (const snippet of mcpSnippets(url, token)) {
      if (snippet.where.endsWith('.json')) {
        expect(() => JSON.parse(snippet.text), snippet.id).not.toThrow()
      }
    }
  })

  it('hands Codex the token through an environment variable', () => {
    const codex = mcpSnippets(url, token).find((s) => s.id === 'codex')
    expect(codex?.text).toContain(`export LEMMARY_MCP_TOKEN="${token}"`)
    expect(codex?.text).toContain(
      `codex mcp add lemmary --url ${url} --bearer-token-env-var LEMMARY_MCP_TOKEN`,
    )
  })

  it('gives Claude Code one shell command', () => {
    const claude = mcpSnippets(url, token).find((s) => s.id === 'claude-code')
    expect(claude?.text).toBe(
      `claude mcp add --transport http lemmary ${url} --header "Authorization: Bearer ${token}"`,
    )
  })
})
