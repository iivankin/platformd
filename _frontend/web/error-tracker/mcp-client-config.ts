const tokenPlaceholder = "<ERROR_TRACKER_API_TOKEN>";

export const mcpClients = [
  {
    hint: "Export ERROR_TRACKER_API_TOKEN, add this to ~/.codex/config.toml, then restart Codex:",
    id: "codex",
    label: "Codex",
    snippet: (endpoint: string) =>
      [
        "[mcp_servers.error_tracker]",
        `url = "${endpoint}"`,
        'bearer_token_env_var = "ERROR_TRACKER_API_TOKEN"',
      ].join("\n"),
  },
  {
    hint: "Export ERROR_TRACKER_API_TOKEN, then run:",
    id: "claude-code",
    label: "Claude Code",
    snippet: (endpoint: string) =>
      [
        "claude mcp add error-tracker \\",
        "  --transport http \\",
        `  ${endpoint} \\`,
        '  -H "Authorization: Bearer $ERROR_TRACKER_API_TOKEN"',
      ].join("\n"),
  },
  {
    hint: "Add to claude_desktop_config.json:",
    id: "claude-desktop",
    label: "Claude Desktop",
    snippet: (endpoint: string) =>
      JSON.stringify(
        {
          mcpServers: {
            "error-tracker": {
              headers: { Authorization: `Bearer ${tokenPlaceholder}` },
              url: endpoint,
            },
          },
        },
        null,
        2
      ),
  },
  {
    hint: "Add to .cursor/mcp.json in your project:",
    id: "cursor",
    label: "Cursor",
    snippet: (endpoint: string) =>
      JSON.stringify(
        {
          mcpServers: {
            "error-tracker": {
              headers: { Authorization: `Bearer ${tokenPlaceholder}` },
              url: endpoint,
            },
          },
        },
        null,
        2
      ),
  },
  {
    hint: "Add to .vscode/mcp.json in your project:",
    id: "vscode",
    label: "VS Code",
    snippet: (endpoint: string) =>
      JSON.stringify(
        {
          servers: {
            "error-tracker": {
              headers: { Authorization: `Bearer ${tokenPlaceholder}` },
              type: "http",
              url: endpoint,
            },
          },
        },
        null,
        2
      ),
  },
  {
    hint: "Add to opencode.json:",
    id: "opencode",
    label: "OpenCode",
    snippet: (endpoint: string) =>
      JSON.stringify(
        {
          mcp: {
            "error-tracker": {
              headers: { Authorization: `Bearer ${tokenPlaceholder}` },
              type: "remote",
              url: endpoint,
            },
          },
        },
        null,
        2
      ),
  },
] as const;

export type MCPClientID = (typeof mcpClients)[number]["id"];
