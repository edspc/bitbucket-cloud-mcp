package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRepositoryToolRules(t *testing.T) {
	for _, tc := range []struct {
		label string
		rules []string
		name  string
		args  map[string]any
		allow bool
	}{
		{"scoped allow", []string{"get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-a"}, true},
		{"scoped allow case", []string{"get_pipeline@Repo-A"}, "get_pipeline", map[string]any{"repo": "repo-a"}, true},
		{"other repository", []string{"get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-b"}, false},
		{"other tool", []string{"get_pipeline@repo-a"}, "list_pipelines", map[string]any{"repo": "repo-a"}, false},
		{"deny only", []string{"!get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-a"}, false},
		{"deny different case", []string{"!get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "Repo-A"}, false},
		{"deny only elsewhere", []string{"!get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-b"}, true},
		{"deny only other tool", []string{"!get_pipeline@repo-a"}, "list_pipelines", map[string]any{"repo": "repo-a"}, true},
		{"wildcard and deny", []string{"*", "!get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-a"}, false},
		{"deny before wildcard", []string{"!get_pipeline@repo-a", "*"}, "get_pipeline", map[string]any{"repo": "repo-a"}, false},
		{"global allow and scoped deny", []string{"get_pipeline", "!get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-a"}, false},
		{"global allow elsewhere", []string{"get_pipeline", "!get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-b"}, true},
		{"global deny wins", []string{"!get_pipeline", "get_pipeline@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-a"}, false},
		{"global deny only", []string{"!get_pipeline"}, "get_pipeline", map[string]any{"repo": "repo-b"}, false},
		{"scoped wildcard", []string{"*@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-a"}, true},
		{"scoped wildcard elsewhere", []string{"*@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-b"}, false},
		{"scoped wildcard deny", []string{"*", "!*@repo-a"}, "get_pipeline", map[string]any{"repo": "repo-a"}, false},
		{"unconditional deny", []string{"*", "!*"}, "get_pipeline", map[string]any{"repo": "repo-a"}, false},
		{"link both allowed", []string{"link_repos_by_key@repo-a", "link_repos_by_key@repo-b"}, "link_repos_by_key", map[string]any{"source_repo": "repo-a", "target_repo": "repo-b"}, true},
		{"link source denied", []string{"*", "!link_repos_by_key@repo-a"}, "link_repos_by_key", map[string]any{"source_repo": "repo-a", "target_repo": "repo-b"}, false},
		{"link target denied", []string{"*", "!link_repos_by_key@repo-b"}, "link_repos_by_key", map[string]any{"source_repo": "repo-a", "target_repo": "repo-b"}, false},
		{"link target only allowed", []string{"link_repos_by_key@repo-b"}, "link_repos_by_key", map[string]any{"source_repo": "repo-a", "target_repo": "repo-b"}, false},
		{"workspace search", []string{"*", "!search_code@repo-a"}, "search_code", map[string]any{"query": "test"}, false},
		{"explicit repository search", []string{"*", "!search_code@repo-a"}, "search_code", map[string]any{"repo": "repo-b", "query": "test"}, true},
		{"workspace variables", []string{"*", "!set_variable@repo-a"}, "set_variable", map[string]any{"scope": "workspace"}, false},
		{"legacy deployment global deny", []string{"*", "!create_deployment"}, "manage_deployment", map[string]any{"repo": "repo-a", "create": true}, false},
		{"legacy deployment scoped deny", []string{"*", "!create_deployment@repo-a"}, "manage_deployment", map[string]any{"repo": "repo-a", "create": true}, false},
		{"legacy deployment scoped deny elsewhere", []string{"*", "!create_deployment@repo-a"}, "manage_deployment", map[string]any{"repo": "repo-b", "create": true}, true},
		{"legacy deployment scoped allow", []string{"create_deployment@repo-a"}, "manage_deployment", map[string]any{"repo": "repo-a", "create": true}, true},
		{"global calls unaffected", []string{"*", "!set_variable@repo-a"}, "list_variables", map[string]any{"scope": "workspace"}, true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			err := (Policy{Tools: tc.rules}).check(tc.name, tc.args)
			if (err == nil) != tc.allow {
				t.Fatalf("allow=%v, err=%v", tc.allow, err)
			}
		})
	}
}

func TestRepositoryToolAllowCannotOverrideRepositoryLimits(t *testing.T) {
	err := (Policy{Tools: []string{"get_pipeline@repo-a"}, Repositories: []string{"repo-b"}}).check("get_pipeline", map[string]any{"repo": "repo-a"})
	if err == nil || !strings.Contains(err.Error(), "repository is not allowed") {
		t.Fatalf("tool allowance bypassed repository restrictions: %v", err)
	}
}

func TestRepositoryToolRulesRejectBeforeRequestsAndConfirmation(t *testing.T) {
	for _, tc := range []struct {
		tool, rule string
		args       map[string]any
	}{
		{"get_pipeline", "!get_pipeline@repo-a", map[string]any{"repo": "repo-a", "pipeline": "1"}},
		{"get_pipeline", "!get_pipeline@repo-a", map[string]any{"repo": "Repo-A", "pipeline": "1"}},
		{"set_variable", "!set_variable@repo-a", map[string]any{"repo": "repo-a", "scope": "repository", "name": "SECRET", "secured": true}},
		{"link_repos_by_token", "!link_repos_by_token@repo-a", tokenArgs(scopeRepository, "", false)},
		{"link_repos_by_key", "!link_repos_by_key@repo-b", keyArgs(false)},
		{"search_code", "!search_code@repo-a", map[string]any{"query": "test"}},
		{"search_repositories", "!search_repositories@repo-a", map[string]any{}},
		{"list_variables", "!list_variables@repo-a", map[string]any{"scope": "workspace"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			cs := connectUUID(t, Policy{Tools: []string{"*", tc.rule}}, func(http.ResponseWriter, *http.Request) {
				t.Error("denied tool reached Bitbucket")
			})
			out, failed := call(t, cs, tc.tool, tc.args)
			if !failed || !strings.Contains(out, "server policy") {
				t.Fatalf("denied tool reached approval or handler: %s", out)
			}
		})
	}
}

func TestRepositoryToolRuleAllowsOnlyMatchingRepository(t *testing.T) {
	f := newFake()
	f.addRun("repo-a", running())
	f.addRun("repo-b", running())
	cs := connectClient(t, newTestServer(t, f, Policy{Tools: []string{"get_pipeline@repo-a"}}), clientOptions(nil), "")
	mustCall(t, cs, "get_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1"})
	if f.pipelineGet != 1 {
		t.Fatal("allowed repository was not queried")
	}
	out, failed := call(t, cs, "get_pipeline", map[string]any{"repo": "repo-b", "pipeline": "1"})
	if !failed || !strings.Contains(out, "server policy") || f.pipelineGet != 1 {
		t.Fatalf("unlisted repository reached Bitbucket: %s", out)
	}
}

func TestRepositoryToolRulesApplyToResourcesAndSubscriptions(t *testing.T) {
	for _, uri := range []string{pipelineURI("repo-a", 1), scopeURIPrefix + "repo-a/branch/main"} {
		f := newFake()
		// The earlier protocol returns subscription errors synchronously.
		cs := connectClient(t, newTestServer(t, f, Policy{Tools: []string{"*", "!get_pipeline@repo-a"}}), clientOptions(nil), "2025-11-25")
		if _, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri}); err == nil || !strings.Contains(err.Error(), "server policy") {
			t.Fatal("resource bypassed repository-specific tool denial")
		}
		if err := cs.Subscribe(t.Context(), &mcp.SubscribeParams{URI: uri}); err == nil || !strings.Contains(err.Error(), "server policy") {
			t.Fatal("subscription bypassed repository-specific tool denial")
		}
	}
}
