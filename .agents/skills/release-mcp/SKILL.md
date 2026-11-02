---
name: release-mcp
description: Prepare and publish a requested bitbucket-cloud-mcp release with the existing version-bump workflow, GoReleaser assets and post-release verification.
---

# Release the MCP server

Read [Change lifecycle](../../rules/lifecycle.md),
[Development](../../../docs/development.md) and [Releases](../../../docs/releases.md).
Publication steps apply when the user has requested a release; existing task
instructions determine authorization, not the presence of this skill.

1. Check the working tree, remote, target branch and current version in
   `cmd/server/cli.go`. Determine which `patch`, `minor` or `major` bump produces
   the requested version; do not silently choose a different version.
2. Verify that all requested features are included in the commits being released,
   including work that may still be on another branch. Check that the intended
   tag and release do not already exist. Never move a published tag to repair a
   release.
3. Prepare and validate changes with the full checks and independent staged-diff
   review required by the lifecycle. Check `.goreleaser.yaml`; use
   `make release-snapshot` when release configuration or packaging changes.
4. Land the validated changes on the intended release branch. Use the existing
   `.github/workflows/release.yml` with the selected bump. It updates the source
   version and CLI docs, tests, commits, tags and publishes; do not manually bump
   the version first and then dispatch another bump.
5. Wait for the Release workflow's final result. If it fails after pushing a tag,
   inspect the existing tag/release and follow the documented recovery path;
   do not redispatch a bump that would create a second version.
6. Verify the published tag and four assets named
   `bitbucket-cloud-mcp-{darwin,linux}-{arm64,amd64}`. Download them, verify their
   SHA-256 digests against published metadata and scan the built artifacts with
   OSV. A local source scan alone cannot verify the Go runtime in a release binary.
7. On a compatible host, verify `--version`, MCP initialization and the expected
   tools through `tools/list`, using offline configuration with no live API calls.
   Confirm that the release tag contains the requested feature commits.
8. Report the release URL and validation. An authorized Homebrew update follows
   publication, using actual released artifacts and checksums in the tap; source
   archive checksums and executable checksums are not interchangeable.
