# CLI

`bitbucket-cloud-mcp --version` prints `bitbucket-cloud-mcp 0.8.0` and exits.
`--help` (or `-h`) prints usage and configuration names. These commands need no
credentials and ignore environment configuration, even malformed policy arrays.
Unknown arguments fail. With no arguments the executable serves MCP over stdio;
only protocol messages go to stdout, with logs on stderr.

The version in `cmd/server/cli.go` is shared by `--version` and MCP `initialize`.
No linker flags or generated files are needed to build a release from a source
archive.
