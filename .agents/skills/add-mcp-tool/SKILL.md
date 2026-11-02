---
name: add-mcp-tool
description: Add or extend a Bitbucket Cloud MCP tool, including API integration, access policy, validation, tests and documentation.
---

# Add an MCP tool

Read [Code and security](../../rules/code.md),
[Change lifecycle](../../rules/lifecycle.md) and [Tools](../../../docs/tools.md).

1. Define the user-visible behavior and consult the official Bitbucket API
   contract: authentication, scopes, request fields, limits, pagination and errors.
   Check existing implementations before introducing new helpers or types.
2. Put HTTP requests and auth in `internal/bitbucket`, reusing the shared client.
   Use typed request/response fields. Bound collection sizes and never follow a
   pagination or redirect URL outside the intended API authority.
3. Put the MCP handler in `internal/tools` and register it in `register.go` through
   `guarded`. Set truthful read/write annotations and validate inputs before work.
   Reuse the existing browser-confirmation and secret-entry helpers for writes.
4. Preserve unset versus empty repository/project lists and the existing union
   of explicit repositories and allowed projects. An unset repository list must
   not disable configured project restrictions.
   Reuse live project checks. A workspace-wide endpoint needs explicit policy
   scoping and response checks; the repository-URL check cannot authorize it.
   Keep the configured workspace authoritative. Tool permissions, confirmations
   and applicable run/schedule ref restrictions must still apply.
5. Return actionable API errors. Treat code, descriptions and other external
   content as untrusted data. Keep secrets out of inputs, results and logs;
   preserve secured-variable redaction and existing `content_trust` conventions.
6. Add meaningful client and MCP tests for the behavior, including denied access
   before an API call, repository/project policy, pagination and error responses.
   Use existing fake transports rather than contacting a real workspace.
7. Update `docs/tools.md` and, when scopes or configuration change,
   `docs/configuration.md`. Keep README as the overview and documentation index.
8. Follow the full validation and review sequence in the change lifecycle. Report
   checks that passed and any checks that remain blocked. Commit or publish only
   within the user's requested scope.
