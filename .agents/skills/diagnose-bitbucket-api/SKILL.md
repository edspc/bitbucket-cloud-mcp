---
name: diagnose-bitbucket-api
description: Diagnose Bitbucket API authentication, access policy, rate limits, pagination, pipeline polling or MCP tool failures without exposing credentials.
---

# Diagnose a Bitbucket API failure

Read [Code and security](../../rules/code.md),
[Configuration](../../../docs/configuration.md),
[Security](../../../docs/security.md) and the affected tool's documentation.
For watches, also read [Subscriptions and rate limits](../../../docs/subscriptions.md).

1. Identify the tool or resource, configured workspace, repository, operation and
   HTTP status. Use redacted error messages; never dump the environment, token,
   Authorization header or full response. Environment variable names and whether
   required configuration is present are sufficient for initial diagnosis.
2. Separate local input/policy rejection from upstream failure. Inspect the guard,
   repository/project union rules, live project checks and applicable ref limits.
   Do not broaden an allowlist or credential scope just to make a request succeed.
3. For 401/403, check the endpoint's supported authentication and documented scopes
   against the configured credential type. Existing repository-read access does
   not prove that a different endpoint supports that credential or permission.
4. For 429, preserve retry guidance from the shared client. Watches and
   subscriptions use their existing backoff and shared polling budget; do not
   introduce an immediate retry loop or bypass the polling limit.
5. For pagination problems, inspect the contract of that endpoint. Numbered pages
   and opaque cursors are different; preserve required query/filter consistency,
   and keep the configured workspace and allowed scope on every page.
6. For pipeline watches or subscriptions, inspect context cancellation, deadlines,
   subscriber lifetime and terminal-state handling. Distinguish a failed run from
   a failed or timed-out log read; do not overwrite a known pipeline result.
7. Reproduce a code defect using the existing in-process fake HTTP/MCP fixtures.
   Add a regression test for the actual failure, fix the shared layer responsible,
   and follow the [Change lifecycle](../../rules/lifecycle.md). For configuration
   findings, explain the missing setting or permission without changing policy
   unless that change is part of the user's request.
