# Setup in Claude Desktop on macOS

[Install the executable](../README.md) and run `command -v bitbucket-cloud-mcp`
to find its absolute path. In Terminal, run `bitbucket-cloud-mcp configure`
to save credentials and access limits in [macOS Keychain](configuration.md#macos-keychain-setup).

In Claude Desktop, open Settings > Developer > Edit Config. Add the server entry
to `~/Library/Application Support/Claude/claude_desktop_config.json`, preserving
any other MCP servers. Replace `command` with the absolute executable path.
If Claude Desktop cancels URL elicitation before showing a dialog, set
`BITBUCKET_LOCAL_BROWSER` as shown.

```json
{
  "mcpServers": {
    "bitbucket-cloud": {
      "command": "/absolute/path/to/bitbucket-cloud-mcp",
      "env": {
        "BITBUCKET_LOCAL_BROWSER": "true"
      }
    }
  }
}
```

When all settings are saved in Keychain, including the browser preference,
`command` is sufficient. Fully quit and restart Claude Desktop after changing
its configuration or updating the executable.
See [browser approval and secret entry](security.md#secret-page).
