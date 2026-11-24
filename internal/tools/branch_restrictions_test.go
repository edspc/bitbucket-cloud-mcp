package tools

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestManageRepositoryBranchRules(t *testing.T) {
	const repoPath = "/2.0/repositories/ws/repo-a"
	const rulesPath = repoPath + "/branch-restrictions"
	const uuidRulesPath = "/2.0/repositories/ws/{01234567-89ab-cdef-0123-456789abcdef}/branch-restrictions"
	var stored map[string]any
	var mutations []string
	cs := connectUUID(t, Policy{ConfirmationTools: []string{"delete_variable"}, Repositories: []string{"repo-a"}, Projects: []string{}}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == repoPath:
			writeJSON(w, map[string]any{"uuid": "{01234567-89ab-cdef-0123-456789abcdef}", "slug": "repo-a"})
		case r.Method == http.MethodGet && r.URL.Path == rulesPath:
			if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("pagelen") != "1" {
				t.Errorf("unexpected pagination query: %s", r.URL.RawQuery)
			}
			writeJSON(w, map[string]any{"values": []any{stored}, "next": "https://other.invalid/page/3"})
		case r.Method == http.MethodGet && (r.URL.Path == rulesPath+"/42" || r.URL.Path == uuidRulesPath+"/42"):
			writeJSON(w, stored)
		case r.Method == http.MethodPost && r.URL.Path == uuidRulesPath:
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				t.Error(err)
			}
			stored["id"] = 42
			writeJSON(w, stored)
			mutations = append(mutations, r.Method)
		case r.Method == http.MethodPut && r.URL.Path == uuidRulesPath+"/42":
			var patch map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Error(err)
			}
			if want := map[string]any{"pipelines_source_branches": []any{}}; !reflect.DeepEqual(patch, want) {
				t.Errorf("patch = %#v, want %#v", patch, want)
			}
			stored["pipelines_source_branches"] = patch["pipelines_source_branches"]
			writeJSON(w, stored)
			mutations = append(mutations, r.Method)
		case r.Method == http.MethodDelete && r.URL.Path == uuidRulesPath+"/42":
			w.WriteHeader(http.StatusNoContent)
			mutations = append(mutations, r.Method)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	run := func(args map[string]any) manageRepositoryResult {
		t.Helper()
		return decodeResult[manageRepositoryResult](t, mustCall(t, cs, "manage_repository", map[string]any{"repo": "repo-a", "branch_restriction": args}))
	}
	out := run(map[string]any{"action": "create", "rule": map[string]any{
		"kind": "push", "pattern": "main", "users": []any{}, "groups": []any{}, "pipelines_source_branches": []string{"release"},
	}})
	if out.BranchRestriction == nil || out.BranchRestriction.ID != 42 || stored["branch_match_kind"] != "glob" ||
		!reflect.DeepEqual(stored["pipelines_source_branches"], []any{"release"}) || !reflect.DeepEqual(stored["users"], []any{}) || !reflect.DeepEqual(stored["groups"], []any{}) {
		t.Fatalf("create result = %+v, payload = %#v", out, stored)
	}
	out = run(map[string]any{"action": "list", "page": 2, "limit": 1})
	if out.BranchRestrictions == nil || len(*out.BranchRestrictions) != 1 || out.NextPage != 3 {
		t.Fatalf("unexpected list result: %+v", out)
	}
	run(map[string]any{"action": "get", "id": 42})
	run(map[string]any{"action": "update", "id": 42, "rule": map[string]any{"pipelines_source_branches": []string{}}})
	out = run(map[string]any{"action": "delete", "id": 42})
	if out.DeletedBranchRestrictionID != 42 || !reflect.DeepEqual(mutations, []string{"POST", "PUT", "DELETE"}) {
		t.Fatalf("delete result = %+v, mutations = %v", out, mutations)
	}
}

func TestManageRepositoryBranchRuleWriteRequiresConfirmation(t *testing.T) {
	cs := connectUUID(t, Policy{}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unconfirmed write reached Bitbucket: %s", r.Method)
		}
		writeJSON(w, map[string]any{"uuid": "{01234567-89ab-cdef-0123-456789abcdef}", "slug": "repo-a"})
	})
	out, failed := call(t, cs, "manage_repository", map[string]any{"repo": "repo-a", "branch_restriction": map[string]any{
		"action": "create", "rule": map[string]any{"kind": "force", "pattern": "main"},
	}})
	if !failed || !strings.Contains(out, "URL elicitation") {
		t.Fatalf("unexpected response: %s", out)
	}
}

func TestManageRepositoryBranchRuleRespectsRepositoryPolicy(t *testing.T) {
	cs := connectUUID(t, Policy{Repositories: []string{"repo-b"}}, func(http.ResponseWriter, *http.Request) {
		t.Error("denied call reached Bitbucket")
	})
	_, failed := call(t, cs, "manage_repository", map[string]any{
		"repo": "repo-a", "branch_restriction": map[string]any{"action": "list"},
	})
	if !failed {
		t.Fatal("allowed list for a denied repository")
	}
}

func TestBranchRuleClearRequiresExplicitEmptyReadBack(t *testing.T) {
	const repo = "/2.0/repositories/ws/repo-a"
	const uuid = "{01234567-89ab-cdef-0123-456789abcdef}"
	const slugRule = repo + "/branch-restrictions/42"
	const pinnedRule = "/2.0/repositories/ws/" + uuid + "/branch-restrictions/42"
	written := false
	cs := connectUUID(t, Policy{ConfirmationTools: []string{"delete_variable"}}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == repo:
			writeJSON(w, map[string]string{"uuid": uuid, "slug": "repo-a"})
		case r.Method == http.MethodGet && (r.URL.Path == slugRule || r.URL.Path == pinnedRule):
			rule := map[string]any{"id": 42, "kind": "push"}
			if !written {
				rule["pipelines_source_branches"] = []string{"release"}
			}
			writeJSON(w, rule)
		case r.Method == http.MethodPut && r.URL.Path == pinnedRule:
			written = true
			writeJSON(w, map[string]any{"id": 42, "kind": "push"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	out, failed := call(t, cs, "manage_repository", map[string]any{"repo": "repo-a", "branch_restriction": map[string]any{
		"action": "update", "id": 42, "rule": map[string]any{"pipelines_source_branches": []string{}},
	}})
	if !written || !failed || !strings.Contains(out, "Bitbucket omitted pipeline source branches") {
		t.Fatalf("write = %v, error = %v, result = %s", written, failed, out)
	}
}
