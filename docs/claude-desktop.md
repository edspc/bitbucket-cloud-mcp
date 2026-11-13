# Setup in Claude Desktop on macOS

Install `bitbucket-cloud-mcp` and run `command -v bitbucket-cloud-mcp` to find its
absolute path. Run `bitbucket-cloud-mcp configure` in Terminal to save the
workspace, email, API token and optional limits in macOS Keychain.

Use `bitbucket-cloud-mcp` v0.8.1 or newer. If Claude Desktop returns `cancelled`
for URL elicitation without showing a page-opening dialog, enable
`BITBUCKET_LOCAL_BROWSER` so the MCP server opens the approval page in your
default browser itself. Older server versions do not support this setting.

In Claude Desktop, open Settings > Developer > Edit Config. Add the server entry
to `~/Library/Application Support/Claude/claude_desktop_config.json`, preserving
any other MCP servers. Replace the command with the absolute path found above:

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

If the server is already configured, add the environment setting to its existing
`env` object and keep the existing command, arguments and credentials source.
Do not copy the API token into this file. If all settings, including local-browser
mode, are already in Keychain, the server entry needs only `command`.

Fully quit and restart Claude Desktop after changing the configuration or binary.
When a tool needs confirmation or a secured value, its local page opens in the
system browser. Review the workspace, operation and arguments, enter the secret
there, and submit the approval checkbox. Only then does the tool continue.
If the tool call is cancelled or the page expires, call the tool again for a new
page. Secrets are never entered into the chat or passed as tool arguments.
