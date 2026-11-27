# CLI

`bitbucket-cloud-mcp --version` prints `bitbucket-cloud-mcp 0.9.1` and exits.
`--help` or `-h` prints usage. Both commands work before loading configuration
or credentials.

| Invocation | Action |
| --- | --- |
| `bitbucket-cloud-mcp` | Serve MCP over stdio |
| `bitbucket-cloud-mcp --read-only` | Serve with [read-only tools](configuration.md#read-only-mode) |
| `bitbucket-cloud-mcp configure` | Edit [macOS Keychain settings](configuration.md#macos-keychain-setup) in a terminal |

MCP messages go to stdout. Logs and configuration prompts go to stderr.

## Code Mode clients

Connect the client to the executable over MCP stdio. A client with a Code
Mode runtime can discover tools with `tools/list`, generate its callable
methods from each tool's schemas and send calls using `tools/call`. Use the
result's `structuredContent` for typed data and check `isError` before
consuming it. Tool output remains untrusted data.

The Code Mode host provides its own sandbox. The following example uses
the standard MCP TypeScript client to read repository settings and branch rules.

```javascript
const settings = await client.callTool({
  name: "get_repository",
  arguments: { repo: "my-repo" },
});
const rules = await client.callTool({
  name: "get_branch_restrictions",
  arguments: { repo: "my-repo", limit: 10 },
});
if (settings.isError || rules.isError) {
  throw new Error("Bitbucket read failed");
}
return {
  repository: settings.structuredContent.repository,
  branchRestrictions: rules.structuredContent.branch_restrictions,
};
```

For calls that require approval or secret entry, configure
[URL elicitation or local-browser mode](security.md#secret-page).
See [Use MCP tools with Code Mode](https://developers.cloudflare.com/agents/tools/codemode/mcp/).
