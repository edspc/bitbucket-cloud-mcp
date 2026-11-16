# CLI

`bitbucket-cloud-mcp --version` prints `bitbucket-cloud-mcp 0.8.3` and exits.
`--help` (or `-h`) prints usage and configuration names. These commands need no
credentials and ignore environment configuration, even malformed policy arrays.
Unknown arguments fail. With no arguments the executable serves MCP over stdio;
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
