package tools

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestManageRepositoryUpdatesRequestedFields(t *testing.T) {
	const uuid = "{01234567-89ab-cdef-0123-456789abcdef}"
	var payload map[string]any
	cs := connectUUID(t, Policy{
		ConfirmationTools: []string{"delete_variable"},
		Tools:             []string{"*", "!run_pipeline@repo-a", "!run_pipeline@new-slug"},
	}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/2.0/repositories/ws/repo-a":
			writeJSON(w, map[string]any{"uuid": uuid, "slug": "repo-a", "project": map[string]string{"key": "OLD", "uuid": "{old}"}})
		case r.Method == http.MethodPut && r.URL.Path == "/2.0/repositories/ws/"+uuid:
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			writeJSON(w, map[string]any{"uuid": uuid, "slug": "new-slug", "language": "go", "project": map[string]string{"key": "NEW"}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	result := decodeResult[manageRepositoryResult](t, mustCall(t, cs, "manage_repository", map[string]any{
		"repo": "repo-a", "slug": "new-slug", "project": "NEW", "description": "", "language": "go",
	}))
	want := map[string]any{"name": "new-slug", "project": map[string]any{"key": "NEW"}, "description": "", "language": "go"}
	if !reflect.DeepEqual(payload, want) || result.Repository.Slug != "new-slug" || result.Repository.Project != "NEW" || result.ContentTrust != "untrusted" {
		t.Fatalf("request = %#v, result = %+v", payload, result)
	}
}

func TestManageRepositoryPreventsUnapprovedOrOutOfPolicyUpdates(t *testing.T) {
	for _, policy := range []Policy{{}, {Repositories: []string{"repo-a"}}} {
		cs := connectUUID(t, policy, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/2.0/repositories/ws/repo-a" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			}
			writeJSON(w, map[string]any{"uuid": "{01234567-89ab-cdef-0123-456789abcdef}", "slug": "repo-a"})
		})
		out, failed := call(t, cs, "manage_repository", map[string]any{"repo": "repo-a", "slug": "new-slug"})
		if !failed {
			t.Fatalf("unexpected update: %s", out)
		}
	}
}

func TestManageRepositoryPreservesScopedToolRulesOnRename(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules []string
	}{
		{"source deny", []string{"*", "!run_pipeline@REPO-A"}},
		{"destination allow", []string{"manage_repository", "run_pipeline@new-slug"}},
		{"destination deny", []string{"*", "!manage_repository@new-slug"}},
		{"source wildcard", []string{"*@repo-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := connectUUID(t, Policy{Tools: tc.rules, ConfirmationTools: []string{"delete_variable"}}, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/2.0/repositories/ws/repo-a" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				writeJSON(w, map[string]string{"uuid": fakeSourceUUID, "slug": "repo-a"})
			})
			out, failed := call(t, cs, "manage_repository", map[string]any{"repo": "repo-a", "slug": "new-slug"})
			if !failed || !strings.Contains(out, "rename would change applicable tool rules") {
				t.Fatalf("rename result = %s, error = %v", out, failed)
			}
		})
	}
}

func TestRepositoryAndRestrictionsReadWithoutWrites(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		cs := connectUUID(t, Policy{ReadOnly: readOnly}, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/2.0/repositories/ws/repo-a":
				for _, field := range []string{"description", "language", "project.key", "slug"} {
					if !strings.Contains(r.URL.Query().Get("fields"), field) {
						t.Errorf("missing requested field %s", field)
					}
				}
				writeJSON(w, map[string]any{"slug": "repo-a", "description": "Example", "language": "go", "project": map[string]string{"key": "APP"}})
			case r.Method == http.MethodGet && r.URL.Path == "/2.0/repositories/ws/repo-a/branch-restrictions":
				writeJSON(w, map[string]any{"values": []map[string]any{{"id": 42, "kind": "push", "pipelines_source_branches": []string{"release"}}}})
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusNotFound)
			}
		})
		name := "manage_repository"
		if readOnly {
			name = "get_repository"
		}
		out := decodeResult[manageRepositoryResult](t, mustCall(t, cs, name, map[string]any{"repo": "repo-a"}))
		if out.Repository == nil || out.Repository.Slug != "repo-a" || out.Repository.Project != "APP" ||
			out.Repository.Description != "Example" || out.Repository.Language != "go" || out.ContentTrust != "untrusted" {
			t.Fatalf("unexpected settings: %+v", out)
		}
		result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "get_branch_restrictions", Arguments: map[string]any{"repo": "repo-a"},
		})
		if err != nil || result.IsError || result.StructuredContent == nil {
			t.Fatalf("missing structured MCP result: %+v, %v", result, err)
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		out = decodeResult[manageRepositoryResult](t, string(data))
		if out.BranchRestrictions == nil || len(*out.BranchRestrictions) != 1 {
			t.Fatalf("unexpected restrictions: %+v", out)
		}
		rule := (*out.BranchRestrictions)[0]
		if rule.PipelinesSourceBranches == nil || !reflect.DeepEqual(*rule.PipelinesSourceBranches, []string{"release"}) {
			t.Fatalf("unexpected pipeline source branches: %+v", rule)
		}
	}
}

func TestManageRepositoryRejectsChangedDestinationProject(t *testing.T) {
	const repoUUID = "{01234567-89ab-cdef-0123-456789abcdef}"
	const projectUUID = "{aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa}"
	lookups := 0
	cs := connectUUID(t, Policy{
		ConfirmationTools: []string{"delete_variable"}, Projects: []string{"OLD", projectUUID},
	}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/2.0/repositories/ws/repo-a":
			writeJSON(w, map[string]any{"uuid": repoUUID, "slug": "repo-a", "project": map[string]string{"key": "OLD"}})
		case r.Method == http.MethodGet && r.URL.Path == "/2.0/workspaces/ws/projects/NEW":
			lookups++
			uuid := projectUUID
			if lookups > 1 {
				uuid = "{bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb}"
			}
			writeJSON(w, map[string]string{"key": "NEW", "uuid": uuid})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	out, failed := call(t, cs, "manage_repository", map[string]any{"repo": "repo-a", "project": "NEW"})
	if lookups != 2 || !failed || !strings.Contains(out, "destination project identity changed") {
		t.Fatalf("lookups = %d, error = %v, result = %s", lookups, failed, out)
	}
}
