# Releases

## Publish a release

Run the [Release workflow](../.github/workflows/release.yml) manually on the
target branch. Set `bump` to `patch` (the default), `minor` or `major`.

The workflow requires the [Security workflow](../.github/workflows/security.yml)
to pass on the selected commit. It then updates the version in
[`cmd/server/cli.go`](../cmd/server/cli.go) and the CLI example, runs tests,
and pushes the version commit and annotated tag atomically. GoReleaser
publishes the binaries and changelog.

The workflow stops if the resulting tag or release already exists. If
publication fails after the version commit and tag are pushed, recover
using that tag. A second bump would create a different version.

## Build locally

Install the Go version in [`go.mod`](../go.mod) and
[GoReleaser](https://goreleaser.com/) v2.18.3, the version pinned by CI.

| Command | Input and output |
| --- | --- |
| `make release` | Clean, tagged checkout whose tag matches the executable version. Builds assets in `dist/` |
| `make release-snapshot` | Any checkout. Uses a `-SNAPSHOT` package version and the committed application version in the executable |

Both commands build locally. The version in `cmd/server/cli.go` is used for
`--version` and MCP `initialize`. The release hook checks the tagged version
and gathers Go and dependency license notices for the release notes.

## Release assets

The [GoReleaser configuration](../.goreleaser.yaml) builds macOS and Linux
executables for amd64 and arm64 with `CGO_ENABLED=0`. The build strips
debug symbols and uses 16-byte function alignment. GitHub serves the raw
executables as `bitbucket-cloud-mcp-{darwin,linux}-{arm64,amd64}`.
Use `dist/artifacts.json` to map local paths to those asset names.

The [Homebrew tap](https://github.com/edspc/homebrew-extended/blob/main/Formula/bitbucket-cloud-mcp.rb)
is updated separately. It builds from the tagged source and publishes its own
bottles. Go selects the Keychain implementation when building for macOS and
environment-only configuration when building for Linux.
