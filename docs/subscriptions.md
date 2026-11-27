# Status subscriptions

Subscribe with `resources/subscribe` or, from MCP 2026-07-28, `subscriptions/listen`.
The server sends `notifications/resources/updated` when the resource changes.
Read the resource for its current value. Subscriptions require repository
access and the `get_pipeline` tool permission for the selected repository.

## Pipeline status

`bitbucket://pipelines/{repo}/{build_number}` returns pipeline status and step
details. A pipeline that completes or pauses for a manual step stops polling.
You can read or subscribe to an already settled pipeline for its current status.

## Branch, pull request and deployment subscriptions

Use `bitbucket://pipeline-scopes/{repo}/{kind}/{selector}` to follow new runs
and status changes. Encode the selector as one path segment. For example,
`feature/new` becomes `feature%2Fnew`.

| Kind | Selector | Resource contents |
| --- | --- | --- |
| `branch` | Branch name | Latest 10 pipelines, newest first, without step details |
| `pullrequest` | Positive PR ID | Latest 10 reported builds, which can include other build providers. `truncated` marks older results |
| `deployment` | Environment UUID, with or without braces | Deployments for the selected environment within the latest 50 repository deployments. `window_limit` and `truncated` describe the window |

For example, use `bitbucket://pipeline-scopes/api/branch/main` or
`bitbucket://pipeline-scopes/api/pullrequest/42`. Get an environment UUID
from `get_uuid`, `manage_deployment` or Bitbucket settings. These subscriptions
keep polling after a run finishes or pauses. For individual step changes,
subscribe to the pipeline itself.

## Rate limit

Subscriptions poll every 30 seconds, or every 60 seconds with project
restrictions. They stop when the last subscriber leaves, after five consecutive
poll failures, or after 24 hours. On repeated failures the server sends a final
update so the client can read the error. Resubscribe to restart polling.
Subscriptions and [`watch_pipeline`](tools.md#watch_pipeline) share two polling
slots per server.

The configured Bitbucket account shares a rolling limit of 1,000 repository-data
requests per hour across tools and subscriptions. A tool call that receives
HTTP `429` returns retry guidance based on `Retry-After` or Bitbucket's
`X-RateLimit-Reset` header. A reset over an hour is ignored. Without a retry
time, the guidance is to try again in a few minutes.

A subscription treats `429` as a pause. It waits at least one poll interval
when the server provides a retry time. Without one, it starts at two intervals
and doubles the pause on consecutive refusals, up to 30 intervals.
