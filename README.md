# bitbucket-cloud-mcp

[MCP](https://modelcontextprotocol.io) server for Bitbucket Cloud. It connects
clients over stdio to repository search, pull requests, Pipelines and
repository settings.

## Install with Homebrew

```sh
brew install edspc/extended/bitbucket-cloud-mcp
```

To update an existing installation, run the following commands.

```sh
brew update
brew upgrade edspc/extended/bitbucket-cloud-mcp
```

## Download the latest binary

Download and install the latest executable from GitHub Releases for macOS or
Linux on ARM64 or x86-64.

```sh
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
curl -fL -o bitbucket-cloud-mcp "https://github.com/edspc/bitbucket-cloud-mcp/releases/latest/download/bitbucket-cloud-mcp-${os}-${arch}" &&
  mkdir -p "$HOME/.local/bin" &&
  install -m 0755 bitbucket-cloud-mcp "$HOME/.local/bin/bitbucket-cloud-mcp"
```

## Build from source

With the Go version in [`go.mod`](go.mod), run `make install` from the
checkout. The default destination is `~/.local/bin`. Set `BINDIR` to choose
another directory.

## Connect an MCP client

Add the install directory to `PATH` or use the executable's absolute path.
Verify the installation with the following command.

```sh
bitbucket-cloud-mcp --version
```

Configure the workspace, Atlassian email and API token, then register the
executable as an MCP stdio server. On macOS, `bitbucket-cloud-mcp configure`
can save the settings in Keychain. See [Configuration](docs/configuration.md).

## Documentation

- [CLI](docs/cli.md)
- [Configuration and access limits](docs/configuration.md)
- [Setup in Codex](docs/codex.md)
- [Setup in Claude Code](docs/claude-code.md)
- [Setup in Claude Desktop](docs/claude-desktop.md)
- [Tools](docs/tools.md)
- [Status subscriptions and rate limits](docs/subscriptions.md)
- [Security and secret entry](docs/security.md)
- [Development and checks](docs/development.md)
- [Releases](docs/releases.md)

[MIT License](LICENSE)
