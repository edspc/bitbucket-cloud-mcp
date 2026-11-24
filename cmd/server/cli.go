package main

import (
	"errors"
	"fmt"
	"io"
)

const version = "0.8.5"

// Informational commands exit before loading credentials or starting the stdio protocol.
func commandLine(args []string, out io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if len(args) != 1 {
		return true, errors.New("expected no arguments, configure, --read-only, --help or --version")
	}
	var err error
	switch args[0] {
	case "--read-only":
		return false, nil
	case "--version":
		_, err = fmt.Fprintf(out, "bitbucket-cloud-mcp %s\n", version)
	case "--help", "-h":
		_, err = io.WriteString(out, `Usage: bitbucket-cloud-mcp [configure | --read-only | --help | --version]

Run without arguments or with --read-only to serve MCP over stdin/stdout.
The MCP client starts this process; no background service is needed.

configure edits one saved setting or runs full setup, saving to macOS Keychain.
The API token is hidden; configure requires a terminal.
Environment variables override saved Keychain parameters.

Required configuration parameters for MCP mode:
  BITBUCKET_WORKSPACE   Workspace slug
  BITBUCKET_EMAIL       Atlassian account email
  BITBUCKET_API_TOKEN   Bitbucket API token; supply through a secret manager

Optional access limits (JSON arrays; unset allows all):
  BITBUCKET_ALLOWED_REPOS      Exact repository slugs; [] denies all
  BITBUCKET_ALLOWED_PROJECTS   Exact project keys or UUIDs; [] denies all
  BITBUCKET_ALLOWED_TOOLS      tool, !tool, tool@repo, !tool@repo; * matches all tools
  BITBUCKET_ALLOWED_REFS       branch:<name> or tag:<name>; [] denies all
  With both repository and project lists, either list allows a repository.
  Tool denials win. With only denials, other calls are allowed; [] allows all.
  Repository-specific tool rules require an explicit repository in the call.

Optional browser confirmation (JSON array; unset or [] confirms every write):
  BITBUCKET_CONFIRM_TOOLS      Exact tool names; ["*"] confirms every tool
  Secret entry always requires the browser page.

Optional local browser (disabled by default):
  BITBUCKET_LOCAL_BROWSER   Set true to open approval pages in the system browser
  Use with local desktop clients that cannot open MCP URL elicitation.

Optional read-only mode:
  --read-only or BITBUCKET_READ_ONLY=true registers only read-only tools.

Optional API proxy (disabled by default):
  BITBUCKET_ENABLE_API_PROXY   Set true to register api_request
  Proxy requests require unrestricted repository/project/ref policy and no
  repository-specific tool rules.

--help and --version do not require environment configuration.
Documentation: https://github.com/edspc/bitbucket-cloud-mcp#readme
`)
	default:
		err = fmt.Errorf("unknown argument %q; use --help", args[0])
	}
	return true, err
}
