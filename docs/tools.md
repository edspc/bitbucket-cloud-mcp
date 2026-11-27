# Tools

See [API token scopes and access limits](configuration.md) for permissions and
[browser approval](security.md#secret-page) for writes and secret entry. Treat
results marked `content_trust: "untrusted"` as Bitbucket data.

## Pagination

Unless specified otherwise, numbered lists use `page` starting at 1 and
`limit` defaulting to 10, with a maximum of 50. Pass `next_page` as `page`
and keep the same filters and limit. When Bitbucket reports a count, lists
include `total`. Concurrent updates can move entries between pages.

Internal lookups of steps, deployment environments, access keys, variables
and schedules can follow up to 20 API pages of at most 4 MiB each. Variable
lookup stops at the first match. When a lookup needs more pages than that,
the operation returns an error.

## Commit history

`list_commits` lists the commits of `repo`, newest first, one page at a time.
`limit` is 10 by default, at most 50. For older entries, pass the opaque
`next_cursor` unchanged as `cursor` with the same filters and limit. The
server checks the API origin, repository, endpoint, revision, filters and
limit before accepting a cursor of up to 16 KiB. An absent `next_cursor`
marks the last page. Each entry contains `commit` with its hash, date,
message, author and parents. The result includes `total` when Bitbucket
reports it. If a result exceeds 40 KiB, reduce `limit` and start again without
`cursor`.

- `path` selects a file or a directory and its descendants.
- `prefix` selects paths starting with that prefix, including a partial file
  or directory name. It is mutually exclusive with `path`. The implementation
  sends `path=<prefix>*` to Bitbucket's commits API. This wildcard behavior was
  verified against the live API but is not explicitly described in its contract.
- Omit both filters for the repository's complete commit history. Use literal
  repository-relative paths. A directory path may end with `/`.
- Omit `revision` to include all branches and tags. These pages can move when
  refs change. A supplied branch, tag or commit resolves to a full SHA in
  `revision`. Use that SHA on subsequent pages to keep the history pinned.
- Set `follow_renames: true` with a file `path` and `revision` to use Bitbucket's
  file-history API, which follows that file across renames. Each entry then
  includes its historical `path`. This option is unavailable with `prefix`.
  Without it, history follows the supplied path, not a file's previous names.

Each call reads one history page, plus a revision lookup when needed.

```json
{"repo":"my-repo","prefix":"src/pay","revision":"main","limit":10}
```

```json
{"repo":"my-repo","path":"src/payment.go","revision":"main","follow_renames":true}
```

## API proxy

`api_request` sends one request to `https://api.bitbucket.org/2.0` with the
configured account and token. Enable it in the [server configuration](configuration.md#optional-api-proxy).
Its required inputs are `method` (uppercase `GET`, `HEAD`,
`OPTIONS`, `POST`, `PUT`, `PATCH` or `DELETE`) and `path`, starting with `/`
relative to that API base, without `/2.0`. Absolute URLs, path traversal and
redirects are refused. Repository, workspace, team and snippet paths must name
the configured workspace. Account endpoints such as `/user` are also available.

`query` accepts arrays of strings for repeated parameters and keeps any
parameters already in `path`. `body` accepts optional JSON for writes.
GET, HEAD and OPTIONS do not accept a body. Custom authentication, headers,
multipart uploads and binary responses are unsupported. The combined path,
query and body are limited to 64 KiB. Response bodies are limited to 40 KiB.

The result contains `status`, `content_type`, and either JSON `body` or UTF-8
`text`. Known credential fields and all JSON fields named `value` are redacted,
including partial variable responses without their secured flag. Configured
credentials are removed from returned strings. Webhook URLs show the scheme,
host and any explicit port. Upstream webhook errors and non-JSON webhook
bodies are withheld because they can echo the full URL. Use dedicated tools
with browser secret entry for secret-bearing operations.

The proxy refuses calls when any repository, project or ref allowlist is set,
including an empty list, or when any repository-specific tool rule is present.
Confirmation shows the method, path, query and body. By default it applies to
all methods except `GET`, `HEAD` and `OPTIONS`.

There is no automatic pagination or retry. Use small `pagelen` and `fields`
values where the endpoint supports them. For another page, pass the API's
`next` URL as `path` after removing `https://api.bitbucket.org/2.0`. API failures
and 429 retry guidance are returned. If a write succeeds but its response is
oversized or unreadable, check its outcome before repeating it.

```json
{"method":"GET","path":"/repositories/my-workspace/my-repo/refs/branches","query":{"pagelen":["10"]}}
```

## Files and directories

`get_file` reads a UTF-8 text file and `list_directory` lists the direct children
of a directory. Both take `repo`, `revision` (a branch, tag or commit hash) and
`path`, relative to the repository root with `/` separators. For a root listing,
pass `path: ""`. Absolute paths, empty path segments, `.` and `..` segments and
backslashes are refused. Paths are not interpreted as local filesystem paths.

A branch or tag is resolved to a full commit SHA before reading. Both results
return that SHA as `commit`. Pass it as `revision` on subsequent calls, along
with `next_offset` as `offset` for `get_file` or `next_page` as `page` for
`list_directory`. Keep the same path and limit to continue reading the same
version.

`get_file` accepts files up to 1 MiB, including empty files, and returns at most
40 KiB per call without splitting UTF-8 characters. `size` and offsets count
bytes. Each call downloads the bounded file again. Binary files (NUL bytes or
invalid UTF-8), larger files and redirected downloads, including Git LFS, are
refused. Directory entries contain their type, repository-relative path,
size and attributes when supplied by Bitbucket.

```json
{"repo":"my-repo","revision":"main","path":"README.md"}
```

## Branches and tags

`list_branches` and `list_tags` take `repo` and return `refs` by name, with
each target commit. Annotated tags can also include a message.
`BITBUCKET_ALLOWED_REFS` applies to pipeline runs and schedule changes.

## Repository search

`search_repositories` finds accessible workspace repositories by a partial
name (`query`) and optional project key (`project`). Omit `query` to list all
matching repositories. Results are sorted by slug. Use that slug as `repo`
in other tools. Each result includes the name, project, description, language,
privacy, main branch, last update and Bitbucket URL.

The request itself asks Bitbucket only for allowed repositories: those listed
in `BITBUCKET_ALLOWED_REPOS`, and, with `BITBUCKET_ALLOWED_PROJECTS`, those in
an allowed project, by key or by UUID for entries in braces. The server then
checks every repository of the answer against the same rule, by the project it
shows, and drops any other. As only the page returned can be checked, `total`,
Bitbucket's count of every page, is left out while access limits are set.
A call uses one API request and skips it when the limits allow nothing.

## Code search

`search_code` performs lexical search via `POST /workspaces/{workspace}/codesearch`
using the configured account and workspace. `query` is required, non-blank and at
most 1024 characters. Omit `repo` to search all accessible repositories allowed
by server policy, or pass an exact slug to narrow the search. Optional `language`, `extension`
(without the dot) and `path` (case-sensitive whole-path wildcard with `*` and `?`)
combine with AND. Searches use default branches only. Semantic search and
arbitrary filter trees, namespace overrides and branch/fork filters are not exposed.

`limit` defaults to 20, at most 100. The result contains `id`, `matched_files`
with file URLs, commit versions, snippets, line ranges and match positions, plus
`pagination`. While `pagination.has_more` is true, pass `id` as `request_id` and
`pagination.next_cursor` as `next_cursor`, together, keeping the query and filters
identical. Bitbucket validates cursor/query consistency. Each call fetches one
page, with no automatic retries. Standard API errors, including permission failures,
429 rate limits and 502 backend failures, are returned to the client.

Bitbucket limits results to repositories the configured account can read. The
server's tool allowlist and configurable read confirmations also apply. With
repository or project access limits, the server injects an OR of exact allowed
repository slugs into the search request. On each call the server resolves
project keys and UUIDs through a filtered listing and discards out-of-scope
entries. Repository entries are allowed independently of their project.
An empty allowed set returns an empty result without calling code search.

The injected scope is sorted and deduplicated to keep filters stable for cursor
pagination. If accessible project repositories change and Bitbucket rejects
the cursor, start the search again.
Before returning code, the server checks every file URL against the requested
workspace, optional repository and resolved allowed set, and checks current project
membership once per returned repository. A disallowed or unverifiable repository
fails the entire page without returning its code. Raw repository metadata is not
returned. Access limits also suppress the approximate `metadata.total_count`.

This API uses an Atlassian API token with Basic authentication. App passwords
and legacy identity-less OAuth credentials are unsupported.

```json
{"query":"handlePayment","language":"go","path":"src/*","limit":20}
```

## Repository settings

`get_repository` reads a repository by its `repo` slug. It returns its name,
slug, project, description, language, privacy, main branch and URL. A call to
`manage_repository` with only `repo` returns the same settings.

To update a repository, call `manage_repository` with at least one of
`slug`, `project` (a project key in the configured workspace),
`description` or `language` (for example, `go`). Use an empty string to clear
the description or language.

Bitbucket derives the slug from the repository name, so changing the slug
also changes the name. The result contains the slug Bitbucket returns and
the resulting URL. Both the current repository and its destination must
satisfy server access policy.
A rename also requires the same repository-specific tool rules on both slugs.

### Repository webhooks

Use `get_repository` with `{"repo":"my-repo","webhook":{}}` to list webhooks.
Pass `page` and `limit` inside `webhook` for pagination, or supply `uuid`
to read one webhook. Results contain the configured event keys, active flag,
description, destination origin and whether a signing secret is set. The
destination shows the scheme, host and any explicit port.

Use `manage_repository` with `webhook.action` set to `list`, `get`,
`create`, `update` or `delete`. Make a separate call for repository
settings or branch restrictions. For creation, provide `settings` containing
`url`, `description`, `active` and a nonempty `events` array. Use
`uuid` for get, update and delete. Updates send only supplied fields,
including `active: false` and empty descriptions. Keep credentials out of
the webhook URL. Set `set_secret: true` to enter a signing secret in the
local browser page, or `remove_secret: true` on update to clear it.

```json
{"repo":"my-repo","webhook":{"action":"create","settings":{"url":"https://example.com/webhook","description":"CI events","active":true,"events":["repo:push"]}}}
```

Bitbucket Cloud provides delivery logging and delivery records through
Repository settings > Webhooks > View requests > Enable history.
Its public API serves webhook subscriptions and their configured event keys.
See [Troubleshoot webhooks](https://support.atlassian.com/bitbucket-cloud/kb/troubleshoot-webhooks/)
for delivery history.

### Git branch restrictions

`get_branch_restrictions` reads rules using `repo` and optional `page` and
`limit`. Supply `id` instead of pagination to read a single rule. The
result includes `pipelines_source_branches` when Bitbucket returns it.

Use `manage_repository` with `repo` and `branch_restriction` to manage
repository-level Git branch restrictions. Make a separate call for a
repository settings update.

- `list` returns a page of rules, including IDs.
- `get` returns the rule with the supplied `id`.
- `create` takes a `rule`. Set `kind` and either a destination `pattern`
  for `glob` (the default) or `branch_match_kind: "branching_model"` with
  `branch_type` such as `production`, `development` or `release`.
- `update` takes an `id` and a `rule` with the fields to change.
- `delete` takes an `id`. Removing a rule lifts that specific restriction.
  Other matching rules can still apply.

For direct write and PR merge exceptions, use `push` and `restrict_merges`.
On creation, supply both `users` (objects with `uuid` or `account_id`) and
`groups` (objects with `slug`). Empty arrays grant no exceptions of that
type. Use `force` to block history rewrites and `delete` to block branch
deletion. A `push` rule can also set `pipelines_source_branches`. These are
branch names on which permitted pipelines run. The rule's `pattern` or
`branch_type` selects the destination branch. The tool reads a rule back
after writing this field to verify that Bitbucket saved it. If Bitbucket
omits the field, the tool reports that the write could not be verified,
even when the requested list was empty.

```json
{"repo":"my-repo","branch_restriction":{"action":"create","rule":{"kind":"push","pattern":"main","users":[],"groups":[],"pipelines_source_branches":["release"]}}}
```

Merge checks and conditions use Bitbucket's `kind` values directly. Pass a
numeric `value` for approval counts, passing builds or maximum commits
behind (including zero). The table shows supported kinds.

| Setting | `kind` |
| --- | --- |
| Minimum approvals | `require_approvals_to_merge` |
| Minimum approvals from default reviewers | `require_default_reviewer_approvals_to_merge` |
| No requested changes | `require_no_changes_requested` |
| No unresolved tasks | `require_tasks_to_be_completed` |
| No unresolved comments | `require_all_comments_resolved` |
| Minimum passing builds | `require_passing_builds_to_merge` |
| Maximum commits behind destination | `require_commits_behind` |
| Reset requested changes on source updates | `reset_pullrequest_changes_requested_on_change` |
| Auto-merge when builds pass | `allow_auto_merge_when_builds_pass` |
| Reset approvals on source updates | `reset_pullrequest_approvals_on_change` |
| Keep approvals when the diff is unchanged | `smart_reset_pullrequest_approvals` |
| Block merges with unresolved checks | `enforce_merge_checks` |

Bitbucket applies workspace plan limits to merge checks.
See the [Bitbucket branch restrictions API](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-branch-restrictions/).

## Repository, deployment and user UUIDs

`get_uuid` returns the UUID of `repo` in the configured workspace. Supply
`environment` with the exact deployment environment name to get that
environment's UUID instead. The result's `uuid` is copied from Bitbucket
without normalization: braces, hyphens and letter case are preserved, for
example `{"content_trust":"untrusted","uuid":"{01234567-89ab-cdef-0123-456789abcdef}"}`.
An unknown environment or a missing UUID is an error.

To get a user's UUID, pass `user` with their Atlassian account ID or Bitbucket
UUID. Use `{"user":"me"}` for the authenticated account. Pass `user` on its
own, without `repo` or `environment`.
Repository and project allowlists do not apply to user lookups. The tool
allowlist and confirmation policy still apply.

## Repository links

Both tools link a source repository A to a target repository B in the
configured workspace. Before changing anything they check that pipelines are
enabled in both repositories, and they report a conflict instead of
overwriting it unless `force` is set.

### `link_repos_by_key`

Adds the public SSH key of A's pipelines to the Access Keys of B, labelled with
A's slug, so A's pipelines can clone B.

| Input         | Meaning                                              |
|---------------|------------------------------------------------------|
| `source_repo` | A                                                    |
| `target_repo` | B                                                    |
| `force`       | Replace an Access Key labelled with A's slug that holds a different key |

If B already has the same key, nothing changes. A must have a pipelines SSH
key (Repository settings → Pipelines → SSH keys).

### `link_repos_by_token`

Stores a repository access token of A as a secured pipeline variable of B.

| Input           | Meaning                                                    |
|-----------------|------------------------------------------------------------|
| `source_repo`   | A                                                          |
| `target_repo`   | B                                                          |
| `variable_name` | Name of the variable in B                                  |
| `scope`         | `repository` (all pipelines) or `deployment`               |
| `environment`   | Deployment environment name in B, with `scope: deployment` |
| `permissions`   | Token permissions: `repository`, `repository:write`, `repository:admin`, `repository:delete`, `pullrequest`, `pullrequest:write`, `webhook`, `pipeline`, `pipeline:write`, `pipeline:variable` |
| `force`         | Overwrite the variable if it exists                        |

Create the token in Bitbucket and enter it on the [secret page](security.md#secret-page).
The page links to A's access token settings and lists the name and permissions
to select. The server probes read access to one endpoint in A chosen from
`permissions`, then stores the token in the approved target repository.
Success reports `token_validation: "read_access_only"`. Verify the token type,
repository scope and requested permissions in Bitbucket before submitting.

## Pipeline variables

The variable tools use `scope` to select where the variables live.

| `scope`      | Other inputs          | Variables of                          |
|--------------|-----------------------|---------------------------------------|
| `workspace`  | —                     | the workspace, for every repository   |
| `repository` | `repo`                | all pipelines of the repository       |
| `deployment` | `repo`, `environment` | one deployment environment of `repo`  |

Deployment-scoped operations require the selected environment's UUID from
Bitbucket. A missing UUID returns an error before any variable is read or changed.

- `list_variables` lists one page of names and plain values. Secured variables
  expose their names and secured flag. Deployment lookups may require
  additional requests to resolve the environment UUID.
- `set_variable` creates a variable (`name`), or overwrites it with
  `force: true`. A plain variable takes its `value` from the call. A secured
  variable (`secured: true`) never does: a `value` in the call is refused, and
  the user enters the value on the [secret page](security.md#secret-page).
- `delete_variable` deletes the variable called `name`.

## Pipelines

### `stop_pipeline`

Pass `repo` and `pipeline` (a build number or UUID) to stop a run and its
unfinished steps. Approval shows the resolved UUID and build number. A changed
target requires fresh approval.

`status: "stop_requested"` means Bitbucket accepted the signal. Check the final
state with `get_pipeline` or `watch_pipeline`. The tool returns
`already_completed` if the run had finished at its final check, or `cancelled`
when approval is declined. Bitbucket may finish a run during the stop request
and return an error.

### `run_pipeline`

| Input              | Meaning                                                          |
|--------------------|------------------------------------------------------------------|
| `repo`             | Repository                                                       |
| `branch` / `tag`   | Exactly one ref to build |
| `commit`           | Commit on the selected ref. Defaults to its latest commit |
| `custom`           | Custom pipeline (`pipelines.custom.<name>`). Defaults to the pipeline defined for the ref |
| `variables`        | Plain variables of the custom pipeline: `[{"name", "value"}]`    |
| `secret_variables` | Names of secured variables for this custom pipeline run. Enter their values on the [secret page](security.md#secret-page) |

The result includes the started pipeline, its build number, URL and
`resource_uri`.

### `list_pipelines`, `get_pipeline`

`list_pipelines` returns pipelines of `repo` newest first. Combine these
filters to narrow the results.

| Filter | Keeps pipelines |
|--------|-----------------|
| `branch` | that built this branch |
| `ref_type` | that built a `branch`, a lightweight `tag` or an `annotated_tag` |
| `commit` | that built this commit, by its full 40-character hash |
| `definition` | of a part of `bitbucket-pipelines.yml`, written as results show it: `custom`, `branches`, `tags`, `pull-requests` or `default`, and for all but `default` optionally a name or pattern, such as `custom: deploy` or `branches: main` |
| `trigger` | started by a `push`, `manual`ly (in Bitbucket or through the API), `scheduled`, or by a `parent_step` (child pipelines) |

The listing includes the `trigger` for each run and omits steps. It fetches
one API page per call.

`get_pipeline` returns one run by build number or UUID (braces are optional)
and a page of steps.
It uses `steps_page`, `steps_limit`, `steps_next_page` and `steps_total` with
the same defaults and limits as [numbered lists](#pagination).
States include `PENDING`, `IN_PROGRESS/RUNNING`, `IN_PROGRESS/PAUSED` and
`COMPLETED/FAILED`. Steps include a Bitbucket URL when a UUID is available.

When a run pauses for manual execution, use its `notice` to share the waiting
step's URL and ask the user to click **Run** in Bitbucket. Use the pipeline URL
if the waiting step's link is unavailable. A pending step alone cannot identify
a manual step. Resume monitoring after the user starts it.

### `watch_pipeline`

Polls the run and all its steps until completion, a manual pause or
`timeout_seconds` (600 by default, at most 3600). The result includes
`outcome` (`completed`, `paused` or `timeout`), observed changes and the last
complete status. The first poll is immediate. Set `poll_interval_seconds`
from 1 to 60 for this call. Omit it or pass zero for 30. Project restrictions
double the interval. Clients that supply a progress token receive a status
notification for each poll.

With `stream_logs: true`, the same progress channel also delivers new log lines
from every started step, including parallel steps. Set `step` to a UUID or
a unique name to follow only that step. The client must send a progress token
for this mode. The following MCP `tools/call` parameters enable it.

```json
{
  "name": "watch_pipeline",
  "arguments": {"repo": "my-repo", "pipeline": "42", "stream_logs": true},
  "_meta": {"progressToken": "pipeline-42"}
}
```

Log progress messages are JSON objects containing
`content_trust: "untrusted"`, `build_number`, `step`, `log` and the step's `url`.
The server uses byte offsets to avoid repeats and strips terminal escapes.
It buffers partial lines until a newline or the end of the watch and performs
a final read for completed steps. It retries missing logs while a step runs.
A log still missing at the end is marked `unavailable: true`.

Each read keeps the latest 64 KiB of raw log and each message carries at most
32 KiB as JSON. `truncated: true` marks a gap or reset. The final `logs` keep
up to 256 KiB of recent chunks, including any partial line at timeout.
`logs_truncated: true` marks omitted output. Use `get_pipeline_log` or the
step URL for more.

The timeout also cancels API requests and progress notifications. If the first
poll times out, the result omits `pipeline`. Client cancellation returns an
error.

A typical status poll takes two Bitbucket requests. Project membership checks
can double the cost. See the [shared concurrency and rate limits](subscriptions.md#rate-limit).

Streaming adds one log API request per unfinished tracked step on each poll.
The wait after that poll is multiplied by `(2 + log requests) / 2` to keep the
same request budget for the selected interval: at the default 30 seconds,
one log read gives 45 seconds, two give 60 seconds, before
the doubling for project restrictions. Status progress heartbeats continue at the
selected base interval (including the doubling for project restrictions) while
waiting, without additional API requests. The server tracks up to 100
started steps.

Short intervals consume the shared allowance faster. The watch returns
`429` with retry guidance when Bitbucket refuses a poll.

```json
{"repo":"my-repo","pipeline":"42","poll_interval_seconds":5,"timeout_seconds":600}
```

### `get_pipeline_log`

Returns the last `tail_lines` lines (200 by default, at most 2000) of a step's
log. Select a step by UUID or unique name. With `step` omitted, the tool
selects the first failed step or, if none failed, the last step that started.
It downloads the end of the log and links to the step page with the whole log.

The `steps` alternatives use the same pagination fields as
[`get_pipeline`](#list_pipelines-get_pipeline). The tool searches the full
step list within the [internal lookup limit](#pagination), so the chosen
log is independent of the displayed page. Ambiguous step names return the
matching UUIDs for selection.

## Deployment environments

Call `manage_deployment` with `repo` and `name` to look up an existing
environment by its exact name. To create a new one, pass `create: true` and
`environment_type` (`Test`, `Staging` or `Production`). Creation requires
confirmation by default and rejects duplicate names. The result includes the
environment name and UUID. Use `run_pipeline` to execute a deployment pipeline.

Configure deployment branch, user and group restrictions in Bitbucket under
Repository settings > Pipelines > Deployments. The tool rejects
`allowed_branches`, `allowed_users` and `allowed_groups`. Track public REST
API support in [BCLOUD-21703](https://jira.atlassian.com/browse/BCLOUD-21703)
and [BCLOUD-24203](https://jira.atlassian.com/browse/BCLOUD-24203).

## Schedules

Bitbucket runs scheduled pipelines on branches only, by a cron pattern in UTC
with 7 fields: seconds, minutes, hours, day of month, month, day of week and
year (`0 0 2 * * ? *` runs at 02:00 UTC every day).

- `list_schedules` lists schedule UUIDs, branches, pipelines, cron patterns
  and enabled flags for `repo`.
- `add_schedule` schedules the `pipeline` of `branch` in `repo` by `cron`. The
  pipeline is written the way results show it: `custom: <name>`,
  `branches: <pattern>` with the pattern as in `bitbucket-pipelines.yml`, or
  `default`. An identical schedule is reported as `already_exists` and not
  added twice. The server serializes the duplicate check and creation after
  browser approval, so concurrent calls in this process cannot create
  identical schedules. Separate server processes and changes made directly in
  Bitbucket do not share this lock.
- `delete_schedule` deletes the `schedule` with the given UUID from
  `list_schedules`. The schedule is read again after browser approval, right
  before it is deleted: if it changed meanwhile (branch, pipeline, cron or
  whether it is enabled) or its branch is no longer allowed, nothing is
  deleted and the call has to be made again.

## Pull requests

### Reading comments

`list_pull_request_comments` reads general comments, inline comments and replies
for `pull_request` (a positive PR ID) in `repo`, oldest first. Each call reads
one page.

Each comment includes its ID, raw Markdown `content`, author, creation/update
times, a Bitbucket link and `deleted`, `pending` and `resolved` flags. A reply has
`parent_id`, even when its parent is on another page. `inline` preserves the file
path, old-side `from` and new-side `to` line numbers and optional range starts
`start_from` / `start_to`. Absent or null line numbers remain absent. Resolution
metadata is returned when available. Deleted comments remain as thread
placeholders with empty content.

An encoded result over 40 KiB returns an error. Reduce `limit` and start
again at page 1 to avoid skipping entries. Read a single oversized comment in
Bitbucket. Upstream pages are bounded to 4 MiB.

```json
{"repo":"my-repo","pull_request":42,"limit":10}
```

### Creating comments and replies

`create_pull_request_comment` posts `content` (raw Markdown) to `pull_request`
in `repo`. Omit `parent_id` or use 0 for a new general PR comment. To reply,
pass a positive comment ID from `list_pull_request_comments` as `parent_id`.
Replies can continue existing inline threads. Use Bitbucket for new inline
threads, edits, deletions and resolution.

Content must be nonblank and at most 32 KiB in UTF-8 bytes. The tool preserves
whitespace. Approval shows the exact text, repository, PR and parent link.
Choose a published, undeleted parent in the same PR. The server rechecks the
PR and parent after approval. If either has changed, read the thread and
request fresh approval. Bitbucket handles that check and posting separately.

Success returns `status: "created"`, `comment_id`, optional `parent_id` and a
Bitbucket URL. Declining returns `status: "cancelled"`. Each approved call posts
once. If a write fails or its response is ambiguous, inspect the discussion
before retrying to avoid duplicate comments. The result omits the comment body.

To post a general comment, pass the repository, PR and text.

```json
{"repo":"my-repo","pull_request":42,"content":"Reviewed: looks good."}
```

Add `parent_id` to reply to an existing comment.

```json
{"repo":"my-repo","pull_request":42,"content":"Fixed in the latest commit.","parent_id":123}
```

### Listing and inspecting pull requests

`list_pull_requests` lists PRs in `repo`, most recently updated first. It
defaults to open PRs. Set `state` to `MERGED`, `DECLINED`, `SUPERSEDED` or
`ALL`, or filter by `source_branch` and `destination_branch`. Results include
the ID, state, draft flag, author, branch and commit details, counts and URL.
Each call reads one API page.

`get_pull_request` takes `repo` and a `pull_request` ID. Its first page
includes the following fields.

- title, description, state (`OPEN`, `MERGED`, `DECLINED` or `SUPERSEDED`),
  draft flag, author, dates, comment and open task counts, how it was closed
  (close source branch, merge commit, closed by, decline reason), and its
  page on bitbucket.org
- the source branch with its commit and repository (another one for a pull
  request from a fork), and the destination branch it merges into
- reviewers and other participants who decided, with their state
  (`approved`, `changes_requested`, or `pending` for a reviewer)
- one page of build results reported for it, newest first, such as pipeline runs
  (50 by default)
- the paths of the changed files with their status and added and removed
  lines, and how many there are, 200 a page. Pass `files_next` back as
  `files_from` for the next page. Later pages leave out the description,
  reviewers and builds.

Build results are fetched in one bounded API request. `builds_limit` is 50 by
default and at most 50. `builds_page` starts at 1. Pass `builds_next_page` as
`builds_page`, keeping the same repository, PR and `builds_limit`, for more.
Later build pages return the PR summary and build results without loading files,
diffs, description or reviewers. Build pagination arguments cannot be combined
with `paths` or `files_from`. New results or PR commits can change the collection
between calls, so results can move or repeat. `builds_truncated` indicates that
later pages exist. `builds_total` is included when Bitbucket reports a count
larger than the returned page.
File pages are fetched directly from the requested offset, with at most three
API page reads per call, rather than downloading all earlier or later pages.
`files_total` is omitted when Bitbucket does not report it. Each API collection
page is limited to 4 MiB.

Pass `paths` to read a diff for selected files. The diff compares the source
branch against the commit where it forked from the destination, as in Bitbucket.
The result includes the PR summary and diff. Diffs over 40 KB are split at line
ends when possible. Pass `diff_next` as `diff_from` with the same `paths` to
continue. Each call downloads the diff up to the end of that part.

If the PR changes, start again without `diff_from` or `files_from` to read the
new version.

Bitbucket returns abbreviated commit hashes in PR metadata and redirects. The
server resolves them to full hashes through the commit API before comparing them.
For merged PRs, the redirect may use the merge commit instead of the source head.
A push between the metadata and redirect requests is refused, and every diff
request and file page uses the checked full commit pair. Continuations are bound
to that actual comparison, including its mode, so a merge that changes the diff
also requires starting again.

These sizes keep a result of ordinary code and paths under the 25,000 tokens
Claude Code allows a tool result by default. Very long paths or content dense
in tokens may exceed that limit. `MAX_MCP_OUTPUT_TOKENS` raises it.

When Bitbucket cannot produce the file list or diff for a large PR, the
result keeps the available fields and adds `files_error` or `diff_error`.
Request fewer `paths` for a smaller diff. A call without `paths` costs
at least four API requests, a later page of files or a part of a diff three.
Abbreviated hashes usually add two commit lookups. Each repository/revision pair
is resolved once per call.
