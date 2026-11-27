# CLI

`bitbucket-cloud-mcp --version` prints `bitbucket-cloud-mcp 0.9.1` and exits.
`--help` (or `-h`) prints usage and configuration names. These commands need no
credentials and ignore environment configuration, even malformed policy arrays.
Unknown arguments fail. With no arguments or with `--read-only` the executable
serves MCP over stdio;
only protocol messages go to stdout, with logs on stderr.

The version in `cmd/server/cli.go` is shared by `--version` and MCP `initialize`.
No linker flags or generated files are needed to build a release from a source
archive.

`bitbucket-cloud-mcp configure` opens a menu when configuration is already saved
in macOS Keychain. Select a single parameter by number or environment-variable name
to change it, for example the API token after rotation or the repository/project
allowlist. All other saved values are preserved. A blank answer keeps the selected
value; `/clear` removes an optional setting to restore its default.
Select `all` to replace the complete configuration, or `0` to cancel.
On first use, the command prompts for every parameter.
API token input is hidden in both modes. See
[Keychain setup](configuration.md#macos-keychain-setup). It exits before starting
MCP and reports success on stderr. A terminal is required; piped JSON is not accepted.

`configure` also asks whether approval pages should open in the system browser
(`BITBUCKET_LOCAL_BROWSER`) for local clients without working URL elicitation.

## Code Mode clients

Connect the client to the executable over MCP stdio. A client with a Code
Mode runtime can discover tools with `tools/list`, generate its callable
methods from each tool's schemas and send calls using `tools/call`. Use the
result's `structuredContent` for typed data and check `isError` before
consuming it. Tool output remains untrusted data.

For a read-only connection, configure this command in the MCP client and
provide the [required environment variables](configuration.md):

```text
bitbucket-cloud-mcp --read-only
```

For example, the Code Mode host can route calls to the MCP connection using
the standard MCP TypeScript client:

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

The Code Mode host supplies the sandbox and invokes the same MCP tools as a
standard client. In a write-enabled connection, configure the host to
support MCP URL elicitation when tools require browser confirmation. See
[Use MCP tools with Code Mode](https://developers.cloudflare.com/agents/tools/codemode/mcp/).
