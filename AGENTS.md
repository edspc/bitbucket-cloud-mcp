# Agent guide

## Project

`bitbucket-cloud-mcp` is a Go MCP server for Bitbucket Cloud. It speaks stdio;
`cmd/server` starts it, `internal/bitbucket` owns API calls, and `internal/tools`
implements tools and resource subscriptions. Installation and user documentation
start at [README.md](README.md).

## Commands

Run from the repository root with the Go version required by `go.mod` or later.

| Task | Command |
| --- | --- |
| Build | `make build` |
| Run | `go run ./cmd/server` |
| Lint | `make lint` |
| Full race tests | `make test` |
| Executable smoke test | `make smoke` |
| Dependency and code scans | `make snyk` |
| Module and binary scans | `make osv` |
| Release snapshot | `make release-snapshot` |

Tool prerequisites and CI details are in [Development](docs/development.md).
The executable is `bin/bitbucket-cloud-mcp` after a local build.

## Rules and task workflows

Read [Repository conventions](.agents/rules/repository.md) and the
[Change lifecycle](.agents/rules/lifecycle.md) before changes. For Go work, also
read [Code and security](.agents/rules/code.md). The rules live in
`.agents/rules/`; task workflows are indexed in [Project skills](.agents/skills/README.md).

- [Add an MCP tool](.agents/skills/add-mcp-tool/SKILL.md)
- [Diagnose Bitbucket API failures](.agents/skills/diagnose-bitbucket-api/SKILL.md)
- [Release the MCP server](.agents/skills/release-mcp/SKILL.md)

`CLAUDE.md` points here. `.claude/rules` and `.claude/skills` point to the same
canonical directories for Claude compatibility.
