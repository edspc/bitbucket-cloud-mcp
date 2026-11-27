# Configuration

Set these values in the server's environment or in [macOS Keychain](#macos-keychain-setup).

| Variable | Value |
| --- | --- |
| `BITBUCKET_WORKSPACE` | Bitbucket workspace slug |
| `BITBUCKET_EMAIL` | Atlassian account email |
| `BITBUCKET_API_TOKEN` | Atlassian API token for Bitbucket Basic authentication |

Environment variables override saved values, including explicitly empty values.
On macOS, a missing Keychain item falls back to the environment. Other Keychain
read errors stop startup to preserve access restrictions. Linux uses the
environment.

Provide the token only to this MCP server, using Keychain or a secret manager
that resolves it at launch. Avoid storing the token in client configuration
or exporting it from a shell profile shared by the MCP client. The
[Claude Code setup](claude-code.md) shows how to use a 1Password reference.

## API token scopes

Select the scopes for the tools you enable. The table lists the scopes used by
the dedicated tools, including preflight reads. The optional API proxy uses
the scopes required by the requested endpoint.

| Operation | API token scopes |
| --- | --- |
| Repository metadata, files, refs, history and commit resolution | `read:repository:bitbucket` |
| Code search | `read:repository:bitbucket`, `read:workspace:bitbucket` |
| User UUID lookup | `read:user:bitbucket` |
| Repository settings updates | `admin:repository:bitbucket`, `read:repository:bitbucket` for identity checks. Add `read:project:bitbucket` if resolving the destination project for a project UUID allowlist |
| Git branch rules | `admin:repository:bitbucket` for reads and writes. Writes also use `read:repository:bitbucket` |
| SSH repository links | `admin:repository:bitbucket`, `read:pipeline:bitbucket`, `write:ssh-key:bitbucket`. Add `delete:ssh-key:bitbucket` for replacement |
| Pipeline, schedule, deployment and variable reads | `read:pipeline:bitbucket` |
| Run or stop pipelines and manage schedules | `read:pipeline:bitbucket`, `write:pipeline:bitbucket`. Running a pipeline or adding a schedule checks Pipelines settings with `admin:repository:bitbucket`. Running a pipeline also resolves its ref with `read:repository:bitbucket` |
| Set or delete pipeline variables and create deployment environments | `read:pipeline:bitbucket`, `admin:pipeline:bitbucket`. Creating an environment also checks Pipelines settings with `admin:repository:bitbucket` |
| Link repositories by token | `admin:repository:bitbucket` to check Pipelines settings, `read:repository:bitbucket` for identity checks, `read:pipeline:bitbucket` and `admin:pipeline:bitbucket` for the destination variable |
| Pull request reads and comments | `read:pullrequest:bitbucket`. PR comparison lookups also use `read:repository:bitbucket` |
| List or read webhooks | `read:webhook:bitbucket` |
| Create, update or delete webhooks | `read:repository:bitbucket` and `read:webhook:bitbucket`. Add `write:webhook:bitbucket` for create or update and `delete:webhook:bitbucket` for delete |

Project restrictions also require repository read access for live membership
checks. Batch repository calls use repository read access to verify each
target before applying any actions. Some webhook events require additional
scopes. Bitbucket's pull request read scope includes posting comments.
See [Atlassian's API token permissions](https://support.atlassian.com/bitbucket-cloud/docs/api-token-permissions/).

## Read-only mode

Start the server with `--read-only` or set `BITBUCKET_READ_ONLY=true`. The flag
takes precedence over `false`. Invalid boolean values stop startup.

This mode registers only read-only tools. Use `get_repository`,
`get_branch_restrictions` and `get_uuid` for reads that correspond to
`manage_repository` and `manage_deployment`. Access rules, configured read
confirmations and pipeline resources still apply.

## Optional API proxy

Set `BITBUCKET_ENABLE_API_PROXY=true` in the operator's environment to register
`api_request`. It defaults to `false`. Read-only mode hides the tool even when
the flag is set. See [API proxy limits and policy](tools.md#api-proxy).

## Local system browser

Set `BITBUCKET_LOCAL_BROWSER=true` for a local desktop client without working
URL elicitation. The server opens the approval page with macOS `open` or Linux
`xdg-open`. This requires a graphical desktop and default browser. The default
uses the MCP client's URL elicitation. See [approval behavior](security.md#local-browser-mode)
and [Claude Desktop setup](claude-desktop.md).

## Confirmation and access limits

Writes require [browser approval](security.md#secret-page) by default.
Set `BITBUCKET_CONFIRM_TOOLS` to an array of exact tool names to require
confirmation only for those tools, including reads if desired. `["*"]`
confirms every tool call. An unset list or `[]` keeps the default. Calls
that need secret entry always use the browser page. Resource reads and
subscriptions apply access limits independently of tool-call confirmation.

Use the following JSON arrays to narrow access. Repository, project and ref
lists match exact names. Invalid arrays stop startup.

| Variable | Example | Restriction |
| --- | --- | --- |
| `BITBUCKET_ALLOWED_REPOS` | `["repo-a","repo-b"]` | Exact repository slugs, regardless of project |
| `BITBUCKET_ALLOWED_PROJECTS` | `["APP","{01234567-89ab-cdef-0123-456789abcdef}"]` | Exact project keys or UUIDs in the workspace |
| `BITBUCKET_ALLOWED_TOOLS` | `["get_pull_request","set_variable@repo-a","!delete_variable@repo-a"]` | [Tool rules](#repository-specific-tool-rules) |
| `BITBUCKET_ALLOWED_REFS` | `["branch:main","tag:v1.2.0"]` | Refs for pipeline runs and schedule changes. A run's commit must belong to its selected ref |

A repository must match at least one entry when either the repository or
project list is set. An empty list adds no repositories. With both lists
unset, the server permits all repositories the token can access. Use project
UUIDs to keep a rule tied to the project across a key change. Setting either
list disables workspace variable tools.

An unset ref list permits all refs. An empty ref list permits none.

For a repository that is allowed only through its project, the server reads
live project metadata before repository requests. This adds an API request and
requires repository read access. Failed lookups stop the operation. Repository
and code search use [filtered workspace listings](tools.md#repository-search).
Bitbucket handles project lookup and writes as separate requests, so an
administrator can still move a repository between them.

Keep policy settings in Keychain, the operator's environment or user-scoped
client configuration. Browser confirmation and tool rules can narrow access
independently of the token's Bitbucket permissions.

### Repository-specific tool rules

| Entry | Meaning |
| --- | --- |
| `get_pipeline` | Allow the tool in every otherwise allowed repository |
| `get_pipeline@repo-a` | Allow the tool only for `repo-a` |
| `!delete_variable@repo-a` | Deny the tool for `repo-a` |
| `!delete_variable` | Deny the tool everywhere |
| `*` or `*@repo-a` | Allow every tool globally or for `repo-a` |
| `!*@repo-a` | Deny every tool for `repo-a` |

A call must match an allow entry when the list contains any allow entry.
With only denials, other tools stay available. An unset list or `[]` allows
all tools. Denials take precedence. Repository slugs in these rules match
case-insensitively, while tool names match case-sensitively. Malformed rules and
partial wildcards stop startup.

Link tools check both repositories. Pipeline resources and subscriptions use
`get_pipeline` rules. A repository-specific rule requires calls to that tool
to name a repository, so it can prevent a workspace variable call or code search
without `repo`. Use global rules for tools without a repository argument,
such as `search_repositories`. Tool rules and confirmation lists accept
`create_deployment` as an alias for `manage_deployment`.

## macOS Keychain setup

Run `bitbucket-cloud-mcp configure` in an interactive terminal. On first use
it prompts for credentials, the five access and confirmation lists, and the
API proxy and local-browser switches. Enter arrays directly, such as
`["repo-a"]`, and use `true` or `false` for switches. Leave optional inputs
blank to use their defaults. Set read-only mode through the command flag or
environment.

If configuration is already saved, select a field by number or parameter name.
For example, choose `BITBUCKET_API_TOKEN` to rotate the token. Blank input keeps
the selected value and `/clear` resets an optional field. Select `all` to
replace the configuration, or `0` to cancel. Terminal echo is disabled for
token entry. Ctrl+C or interrupted input cancels the edit.

The editor validates the complete configuration before writing the
`bitbucket-cloud-mcp` generic-password item with account `configuration`
in the default Keychain. The 4,095-byte limit includes escaping and command
overhead. Oversized updates leave the saved item intact. macOS may ask you
to unlock or authorize access to the Keychain. The editor sends the protected
values to the Keychain utility through stdin.

Start or restart the MCP server or desktop client to load saved settings.
Remove environment overrides for values you want to read from Keychain.
