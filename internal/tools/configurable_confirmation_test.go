package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConfirmationPolicyAllowsWritesWithoutElicitation(t *testing.T) {
	f := newFake()
	s := newTestServer(t, f, Policy{ConfirmationTools: []string{"delete_variable"}})
	cs := connectClient(t, s, clientOptions(nil), "")
	mustCall(t, cs, "set_variable", map[string]any{"scope": "repository", "repo": "repo-a", "name": "PLAIN", "value": "value"})
	mustCall(t, cs, "run_pipeline", map[string]any{"repo": "repo-a", "branch": "main"})
	mustCall(t, cs, "add_schedule", map[string]any{"repo": "repo-a", "branch": "main", "pipeline": "default", "cron": "0 0 2 * * ? *"})
	mustCall(t, cs, "manage_deployment", map[string]any{"repo": "repo-a", "name": "Preview", "create": true, "environment_type": "Test"})
	if f.pages.served() {
		t.Fatal("unexpected browser page")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.runs["repo-a"]) != 1 || f.runs["repo-a"][0].Target.Commit.Hash != testPipelineCommit {
		t.Fatal("pipeline did not pin the commit")
	}
	if len(f.envs["repo-a"]) != 1 || len(f.schedules["repo-a"]) != 1 {
		t.Fatal("missing changes")
	}
}

func TestConfirmationPolicyNeverBypassesSecretsOrAccessLimits(t *testing.T) {
	f := newFake()
	cs := connectClient(t, newTestServer(t, f, Policy{ConfirmationTools: []string{"delete_variable"}, Repositories: []string{"repo-a"}}), clientOptions(nil), "")
	for _, args := range []map[string]any{
		{"scope": "repository", "repo": "repo-a", "name": "SECRET", "secured": true},
		{"scope": "repository", "repo": "repo-b", "name": "PLAIN", "value": "value"},
	} {
		if _, failed := call(t, cs, "set_variable", args); !failed {
			t.Fatal("expected refusal")
		}
	}
	if len(f.mutationList()) != 0 {
		t.Fatal("unexpected changes")
	}
}

func TestConfirmationPolicyCanRequireReads(t *testing.T) {
	f := newFake()
	cs := connectClient(t, newTestServer(t, f, Policy{ConfirmationTools: []string{"list_pipelines"}}), clientOptions(nil), "")
	out, failed := call(t, cs, "list_pipelines", map[string]any{"repo": "repo-a"})
	if !failed || !strings.Contains(out, "URL elicitation") {
		t.Fatalf("read bypassed confirmation: %s", out)
	}
}

func TestManageDeploymentNeedsApprovalAndRejectsDuplicates(t *testing.T) {
	f := newFake()
	cs := connectApproving(t, f)
	args := map[string]any{"repo": "repo-a", "name": "Preview", "create": true, "environment_type": "Staging"}
	out := mustCall(t, cs, "manage_deployment", args)
	if !strings.Contains(out, `"created"`) || !strings.Contains(f.pages.openedPage(), "Preview") {
		t.Fatal(out)
	}
	if _, failed := call(t, cs, "manage_deployment", args); !failed {
		t.Fatal("duplicate accepted")
	}
	args["environment_type"] = "invalid"
	if _, failed := call(t, cs, "manage_deployment", args); !failed {
		t.Fatal("invalid type accepted")
	}
}

func TestSelectedReadConfirmation(t *testing.T) {
	f := newFake()
	user := &fakeUser{pages: f.pages, action: "accept"}
	cs := connectClient(t, newTestServer(t, f, Policy{ConfirmationTools: []string{"list_pipelines"}}), clientOptions(user.elicit), "")
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_pipelines", Arguments: map[string]any{"repo": "repo-a"}})
	if err != nil || !f.pages.served() {
		t.Fatalf("read confirmation: %v", err)
	}
}

func TestEmptyToolListsKeepDefaults(t *testing.T) {
	f := newFake()
	cs := connectClient(t, newTestServer(t, f, Policy{Tools: []string{}, ConfirmationTools: []string{}}), clientOptions(nil), "")
	mustCall(t, cs, "list_pipelines", map[string]any{"repo": "repo-a"})
	out, failed := call(t, cs, "set_variable", map[string]any{"scope": "repository", "repo": "repo-a", "name": "PLAIN", "value": "value"})
	if !failed || !strings.Contains(out, "URL elicitation") || len(f.mutationList()) != 0 {
		t.Fatalf("empty confirmation list bypassed write approval: %s", out)
	}
}

func TestWildcardConfirmsReadsAndWrites(t *testing.T) {
	f := newFake()
	cs := connectClient(t, newTestServer(t, f, Policy{Tools: []string{"*"}, ConfirmationTools: []string{"*"}}), clientOptions(nil), "")
	for _, name := range []string{"list_pipelines", "manage_deployment"} {
		args := map[string]any{"repo": "repo-a"}
		if name == "manage_deployment" {
			args["name"] = "Preview"
			args["create"] = true
			args["environment_type"] = "Test"
		}
		out, failed := call(t, cs, name, args)
		if !failed || !strings.Contains(out, "URL elicitation") {
			t.Fatalf("wildcard omitted confirmation for %s: %s", name, out)
		}
	}
	if len(f.mutationList()) != 0 {
		t.Fatal("unapproved change")
	}
}

func TestManageDeploymentWithoutCreate(t *testing.T) {
	f := newFake()
	writer := connectApproving(t, f)
	mustCall(t, writer, "manage_deployment", map[string]any{
		"repo": "repo-a", "name": "Preview", "environment_type": "Test", "create": true,
	})
	before := len(f.mutationList())
	reader := connectClient(t, newTestServer(t, f, Policy{}), clientOptions(nil), "")
	for _, args := range []map[string]any{
		{"repo": "repo-a", "name": "Preview"},
		{"repo": "repo-a", "name": "Preview", "create": false},
	} {
		out := decodeResult[manageDeploymentResult](t, mustCall(t, reader, "manage_deployment", args))
		if out.Status != "existing" || out.Environment == nil || out.Environment.Name != "Preview" {
			t.Fatalf("unexpected existing environment: %+v", out)
		}
	}
	if _, failed := call(t, reader, "manage_deployment", map[string]any{"repo": "repo-a", "name": "Missing"}); !failed {
		t.Fatal("missing environment should require explicit creation")
	}
	for _, field := range []string{"allowed_branches", "allowed_users", "allowed_groups"} {
		args := map[string]any{"repo": "repo-a", "name": "Restricted", "environment_type": "Test", "create": true, field: []string{}}
		out, failed := call(t, reader, "manage_deployment", args)
		if !failed || !strings.Contains(out, "public API") {
			t.Fatalf("unsupported %s: %s", field, out)
		}
	}
	if len(f.mutationList()) != before {
		t.Fatal("unexpected deployment changes")
	}
}

func TestRenamedDeploymentToolRetainsLegacyConfirmation(t *testing.T) {
	f := newFake()
	policy := Policy{ConfirmationTools: []string{"create_deployment"}}
	cs := connectClient(t, newTestServer(t, f, policy), clientOptions(nil), "")
	out, failed := call(t, cs, "manage_deployment", map[string]any{
		"repo": "repo-a", "name": "Preview", "environment_type": "Test", "create": true,
	})
	if !failed || !strings.Contains(out, "URL elicitation") || len(f.mutationList()) != 0 {
		t.Fatalf("legacy setting did not require confirmation: %s", out)
	}
}
