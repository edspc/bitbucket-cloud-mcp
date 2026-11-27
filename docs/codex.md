# Setup in Codex

Use this setup with Codex CLI, the desktop app or the IDE extension on the
machine where the MCP process will run. [Install the executable](../README.md)
and use `command -v bitbucket-cloud-mcp` to find its absolute path. The local
clients share `~/.codex/config.toml`. [Codex MCP configuration](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)

## macOS Keychain

Save the connection settings with
[`bitbucket-cloud-mcp configure`](configuration.md#macos-keychain-setup).
Replace the path below with the installed executable's absolute path.

```sh
codex mcp add bitbucket-cloud -- /absolute/path/to/bitbucket-cloud-mcp
```

If you use the desktop app without the CLI, add a **STDIO** server in
Settings > MCP servers. Use `bitbucket-cloud` as the name and the executable's
absolute path as the command. Save the configuration and restart.

## 1Password on macOS or Linux

Sign in to the [1Password CLI](https://developer.1password.com/docs/cli/) and
copy the token's secret reference. Add this entry to `~/.codex/config.toml`.
Replace both executable paths, the workspace, the email and the reference.
The config stores the reference and `op run` resolves it at launch.
`--no-masking` preserves the MCP protocol on stdout.

```toml
[mcp_servers.bitbucket-cloud]
command = "/absolute/path/to/op"
args = ["run", "--no-masking", "--", "/absolute/path/to/bitbucket-cloud-mcp"]

[mcp_servers.bitbucket-cloud.env]
BITBUCKET_WORKSPACE = "<workspace>"
BITBUCKET_EMAIL = "<atlassian-email>"
BITBUCKET_API_TOKEN = "op://<vault>/<item>/<field>"
```

## Check the connection

Use `codex mcp list` to check registration. Open a new session and use
`/mcp` to check the connection.

To [restrict the server to reads](configuration.md#read-only-mode), add
`--read-only` after the `bitbucket-cloud-mcp` executable in the command or
argument list. For writes, use [browser approval](security.md#secret-page).
When the client cannot open the approval page, set
`BITBUCKET_LOCAL_BROWSER = "true"` in the server's `env` table.
If Codex times out before an approval or pipeline watch finishes, set
`tool_timeout_sec` in `[mcp_servers.bitbucket-cloud]` to cover the expected wait.
