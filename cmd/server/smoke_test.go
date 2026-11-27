package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercise the executable, including its stdout framing and process shutdown, as installed
// by a package manager. Fake credentials and deny-all policies keep this check offline.
func TestInstalledBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "bitbucket-cloud-mcp")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "BITBUCKET_") {
			env = append(env, entry)
		}
	}
	for _, arg := range []string{"--help", "-h", "--version", "--unknown"} {
		t.Run(arg, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, binary, arg)
			// Even invalid configuration must not affect informational commands.
			cmd.Env = append(append([]string{}, env...), "BITBUCKET_ALLOWED_PROJECTS=invalid")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if arg == "--unknown" {
				if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown argument") {
					t.Fatalf("unknown argument: %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
				}
				return
			}
			if err != nil || stderr.Len() != 0 {
				t.Fatalf("informational command: %v, stderr=%q", err, stderr.String())
			}
			if arg == "--version" && stdout.String() != "bitbucket-cloud-mcp "+version+"\n" || arg != "--version" && !strings.Contains(stdout.String(), "Usage:") {
				t.Fatalf("unexpected output: %q", stdout.String())
			}
		})
	}
	if runtime.GOOS != "darwin" {
		cmd := exec.CommandContext(ctx, binary, "configure")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), "configure requires macOS Keychain") {
			t.Fatalf("configure on %s: %v, stdout=%q, stderr=%q", runtime.GOOS, err, stdout.String(), stderr.String())
		}
	}
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = append(env,
		"BITBUCKET_WORKSPACE=offline-test", "BITBUCKET_EMAIL=test@example.invalid", "BITBUCKET_API_TOKEN=not-a-real-token",
		"BITBUCKET_ALLOWED_REPOS=[]", "BITBUCKET_ALLOWED_PROJECTS=[]", "BITBUCKET_ALLOWED_TOOLS=[]", "BITBUCKET_ALLOWED_REFS=[]")
	client := mcp.NewClient(&mcp.Implementation{Name: "package-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if info := session.InitializeResult().ServerInfo; info.Name != "bitbucket-cloud-mcp" || info.Version != version {
		t.Fatalf("server info = %+v", info)
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) == 0 {
		t.Fatalf("list tools: %+v, %v", list, err)
	}
	foundHistory := false
	foundComments := false
	foundCommentWriter := false
	for _, tool := range list.Tools {
		if tool.Name == "create_pull_request_comment" {
			foundCommentWriter = true
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint {
				t.Fatal("PR comment writer must be marked as a non-idempotent write")
			}
		}
		if tool.Name == "api_request" {
			t.Fatal("API proxy registered without explicit opt-in")
		}
		if tool.Name == "list_commits" {
			foundHistory = true
		}
		if tool.Name == "list_pull_request_comments" {
			foundComments = true
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatal("PR comment reader must be marked read-only")
			}
		}
	}
	if !foundHistory {
		t.Fatal("commit history tool is missing")
	}
	if !foundComments {
		t.Fatal("PR comment reader is missing")
	}
	if !foundCommentWriter {
		t.Fatal("PR comment writer is missing")
	}
	if err = session.Close(); err != nil {
		t.Fatalf("stdio shutdown: %v", err)
	}
	for _, mode := range []string{"flag", "env"} {
		t.Run("read-only/"+mode, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, binary)
			cmd.Env = append(append([]string{}, env...),
				"BITBUCKET_WORKSPACE=offline-test", "BITBUCKET_EMAIL=test@example.invalid", "BITBUCKET_API_TOKEN=not-a-real-token",
				"BITBUCKET_ENABLE_API_PROXY=true", `BITBUCKET_ALLOWED_TOOLS=["*"]`)
			if mode == "flag" {
				cmd.Args = append(cmd.Args, "--read-only")
				cmd.Env = append(cmd.Env, "BITBUCKET_READ_ONLY=false")
			} else {
				cmd.Env = append(cmd.Env, "BITBUCKET_READ_ONLY=true")
			}
			session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			list, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			for _, tool := range list.Tools {
				if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
					t.Errorf("non-read-only tool advertised: %s", tool.Name)
				}
				if (tool.Name == "get_repository" || tool.Name == "get_branch_restrictions") && (tool.InputSchema == nil || tool.OutputSchema == nil) {
					t.Errorf("missing Code Mode discovery schema: %s", tool.Name)
				}
				found[tool.Name] = true
			}
			for _, name := range []string{"get_repository", "get_branch_restrictions"} {
				if !found[name] {
					t.Errorf("missing read tool: %s", name)
				}
			}
			for _, name := range []string{"api_request", "manage_repository", "manage_deployment", "run_pipeline", "create_pull_request_comment"} {
				if found[name] {
					t.Errorf("write-capable tool advertised: %s", name)
				}
				result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
				if err == nil && (result == nil || !result.IsError) {
					t.Errorf("write-capable tool callable: %s", name)
				}
			}
		})
	}

}
