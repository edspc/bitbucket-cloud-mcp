# Development

Run commands from the repository root with the Go version in [`go.mod`](../go.mod)
or later. The server uses [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)
for MCP.

| Task | Command |
| --- | --- |
| Run from source | `go run ./cmd/server` |
| Build the executable | `make build` writes `bin/bitbucket-cloud-mcp` |
| Lint | `make lint` |
| Race tests | `make test` |
| Executable smoke test | `make smoke` starts the server and lists tools offline |
| Snyk | `make snyk` scans dependencies and code |
| OSV | `make osv` scans module versions and the Go runtime linked into the executable |

See the [installation guide](../README.md#build-from-source) for `make install`,
[Releases](releases.md#build-locally) for release builds and the
[change lifecycle](../.agents/rules/lifecycle.md) for pre-commit checks.

## Check prerequisites

Install the versions of golangci-lint and OSV-Scanner used by CI and put
`$(go env GOPATH)/bin` on `PATH`.

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
go install github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.6.0
```

Use the repository's Go version or later to build golangci-lint.
For `make snyk`, install the
[Snyk CLI](https://docs.snyk.io/snyk-cli/install-or-update-the-snyk-cli)
and authenticate with `snyk auth` or `SNYK_TOKEN`. Enable Snyk Code in the
organization. OSV checks all known vulnerabilities in the modules and built
server, including unreachable code.

## Continuous integration

[CI](../.github/workflows/ci.yml) runs separate lint and race test jobs
on pushes to `main` and on pull requests. The test matrix covers Linux
and macOS on Apple Silicon and Intel.

[Security](../.github/workflows/security.yml) runs Snyk and OSV. It pins
actions by commit, the scanner image by digest and the Snyk CLI by checksum.
The Snyk job uses the repository's `SNYK_TOKEN` secret.

[Release check](../.github/workflows/release-check.yml) runs on pull requests
that change release packaging, README or documentation. It builds snapshot
binaries, checks release assets and license notices, and exercises the version
bump. It checks that the release and dry run use matching tool pins.

## Keychain and terminal tests

On macOS, the tests use temporary Keychains and delete them during cleanup.
They cover saving and editing settings, environment overrides, and read
failures, including preservation of the saved item after an oversized edit.
The terminal regression test uses Python 3's `pty` module to verify that
token input is hidden and terminal settings are restored after submission
or Ctrl+C. It skips when Python 3 is unavailable.
