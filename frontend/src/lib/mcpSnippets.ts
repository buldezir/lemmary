export type MCPSnippet = {
  id: string
  name: string
  /** Where the text goes: a shell, or a config file by path. */
  where: string
  text: string
}

const SERVER = 'lemmary'

/**
 * Ready-to-paste configuration for the agents people actually run, in the
 * shape each one documents today. Key names drift between releases, so the
 * page points at the agent's own docs when a snippet stops working.
 */
export function mcpSnippets(url: string, token: string): MCPSnippet[] {
  const bearer = `Bearer ${token}`
  const headers = { Authorization: bearer }
  const json = (value: unknown) => JSON.stringify(value, null, 2)
  return [
    {
      id: 'claude-code',
      name: 'Claude Code',
      where: 'Terminal',
      text: `claude mcp add --transport http ${SERVER} ${url} --header "Authorization: ${bearer}"`,
    },
    {
      id: 'codex',
      name: 'Codex CLI',
      where: 'Terminal (keep the export in your shell profile)',
      // Codex reads a bearer token from an environment variable, never from
      // the command line, so the token lives in the shell and the config
      // names the variable.
      text: [
        `export LEMMARY_MCP_TOKEN="${token}"`,
        `codex mcp add ${SERVER} --url ${url} --bearer-token-env-var LEMMARY_MCP_TOKEN`,
      ].join('\n'),
    },
    {
      id: 'gemini',
      name: 'Gemini CLI',
      where: '~/.gemini/settings.json',
      text: json({ mcpServers: { [SERVER]: { httpUrl: url, headers } } }),
    },
    {
      id: 'cursor',
      name: 'Cursor',
      where: '~/.cursor/mcp.json',
      text: json({ mcpServers: { [SERVER]: { url, headers } } }),
    },
    {
      id: 'vscode',
      name: 'VS Code (Copilot)',
      where: '.vscode/mcp.json',
      text: json({ servers: { [SERVER]: { type: 'http', url, headers } } }),
    },
    {
      id: 'windsurf',
      name: 'Windsurf',
      where: '~/.codeium/windsurf/mcp_config.json',
      text: json({ mcpServers: { [SERVER]: { serverUrl: url, headers } } }),
    },
    {
      id: 'opencode',
      name: 'OpenCode',
      where: 'opencode.json',
      text: json({ mcp: { [SERVER]: { type: 'remote', url, headers, enabled: true } } }),
    },
    {
      id: 'claude-desktop',
      name: 'Claude Desktop',
      where: 'claude_desktop_config.json',
      // Desktop config only launches local commands; mcp-remote bridges to
      // an HTTP server and forwards the header.
      text: json({
        mcpServers: {
          [SERVER]: {
            command: 'npx',
            args: ['-y', 'mcp-remote', url, '--header', `Authorization: ${bearer}`],
          },
        },
      }),
    },
  ]
}
