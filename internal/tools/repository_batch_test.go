package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const batchRepoPath = "/2.0/repositories/ws/"

func serveBatchRepo(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case batchRepoPath + "repo-a":
		writeJSON(w, map[string]string{"uuid": fakeSourceUUID, "slug": "repo-a"})
	case batchRepoPath + "repo-b":
		writeJSON(w, map[string]string{"uuid": fakeTargetUUID, "slug": "repo-b"})
	default:
		return false
	}
	return true
}

func batchRule(kind string) map[string]any {
	return map[string]any{"branch_restriction": map[string]any{
		"action": "create", "rule": map[string]any{"kind": kind, "pattern": "main"},
	}}
}

func TestManageRepositoryBatchUsesOneApprovalAndOrderedUUIDWrites(t *testing.T) {
	f := newFake()
	cs := connectRetryingItself(t, f)
	type mutation struct{ repo, action, secret string }
	var writes []mutation
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveBatchRepo(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		repo, endpoint, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, batchRepoPath), "/")
		switch endpoint {
		case "branch-restrictions":
			var fields bitbucket.BranchRuleFields
			if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
				t.Error(err)
			}
			if fields.BranchMatchKind != "glob" || fields.Pattern == nil || *fields.Pattern != "main" {
				t.Errorf("branch fields = %+v", fields)
			}
			writes = append(writes, mutation{repo, fields.Kind, ""})
			writeJSON(w, bitbucket.BranchRule{ID: len(writes), BranchRuleFields: fields})
		case "hooks":
			var payload struct {
				URL, Description, Secret string
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			writes = append(writes, mutation{repo, payload.Description, payload.Secret})
			writeJSON(w, map[string]any{"url": payload.URL, "description": payload.Description, "secret": payload.Secret})
		default:
			t.Errorf("unexpected write target: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	f.tools.bb = bitbucket.New(&http.Client{Transport: handlerTransport{handler}}, bitbucket.Config{Workspace: "ws"})
	hook := func(description string) map[string]any {
		return map[string]any{"webhook": map[string]any{"action": "create", "set_secret": true,
			"settings": map[string]any{"url": "https://example.invalid/hook", "description": description,
				"active": true, "events": []string{"repo:push"}},
		}}
	}
	args := map[string]any{"repos": []string{"repo-a", "repo-b"},
		"actions": []map[string]any{batchRule("force"), batchRule("delete"), hook("first"), hook("second")},
	}
	first, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_repository", Arguments: args})
	if err != nil || first.IsError || len(first.InputRequests) != 1 || len(writes) != 0 {
		t.Fatalf("initial batch approval = %+v, %v, writes = %d", first, err, len(writes))
	}
	prompt := first.InputRequests[secretsInputID].(*mcp.ElicitParams)
	if status := f.pages.submit(prompt.URL, url.Values{
		"webhook_secret_3": {"first-marker"}, "webhook_secret_4": {"second-marker"},
	}); status != http.StatusSeeOther {
		t.Fatalf("form submission returned %d", status)
	}
	if shown := f.pages.openedPage(); !strings.Contains(shown, fakeSourceUUID) || !strings.Contains(shown, fakeTargetUUID) {
		t.Fatal("batch approval omitted a repository UUID")
	}
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_repository", Arguments: args, RequestState: first.RequestState,
		InputResponses: mcp.InputResponseMap{secretsInputID: &mcp.ElicitResult{Action: "accept"}},
	})
	if err != nil || res.IsError {
		t.Fatalf("batch call = %+v, %v", res, err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	out := decodeResult[manageRepositoryResult](t, text)
	if out.Status != "completed" || len(out.Results) != 8 || strings.Contains(text, "-marker") || strings.Contains(text, "invalid/hook") {
		t.Fatalf("unexpected or unsafe batch result: %+v", out)
	}
	var want []mutation
	for _, uuid := range []string{fakeSourceUUID, fakeTargetUUID} {
		want = append(want, mutation{uuid, "force", ""}, mutation{uuid, "delete", ""},
			mutation{uuid, "first", "first-marker"}, mutation{uuid, "second", "second-marker"})
	}
	if !slices.Equal(writes, want) || f.pages.stopCount() != 1 {
		t.Fatalf("wrong write order, secret assignment or number of approval pages (writes = %d)", len(writes))
	}
	for i, step := range out.Results {
		wantRepo := "repo-a"
		if i >= 4 {
			wantRepo = "repo-b"
		}
		if step.Repo != wantRepo || step.Action != i%4+1 || step.Status != "completed" || step.Result == nil {
			t.Errorf("unexpected batch entry %d: %+v", i, step)
		}
	}
}

func TestManageRepositoryBatchAccepts100WebhookSecrets(t *testing.T) {
	f := newFake()
	cs := connectRetryingItself(t, f)
	secret := strings.Repeat("🙂", 128)
	writes := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveBatchRepo(w, r) {
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != batchRepoPath+fakeSourceUUID+"/hooks" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var change bitbucket.WebhookWrite
		if err := json.NewDecoder(r.Body).Decode(&change); err != nil || change.Secret == nil || *change.Secret != secret {
			t.Error("webhook request did not include the submitted signing secret")
		}
		writes++
		writeJSON(w, bitbucket.Webhook{})
	})
	f.tools.bb = bitbucket.New(&http.Client{Transport: handlerTransport{handler}}, bitbucket.Config{Workspace: "ws"})
	actions := make([]map[string]any, 100)
	values := url.Values{}
	for i := range actions {
		actions[i] = map[string]any{"webhook": map[string]any{"action": "create", "set_secret": true,
			"settings": map[string]any{"url": fmt.Sprintf("https://example.invalid/hooks/%d", i+1), "description": "CI",
				"active": true, "events": []string{"repo:push"}},
		}}
		values.Set(fmt.Sprintf("webhook_secret_%d", i+1), secret)
	}
	if len(values.Encode()) <= maxSecretForm {
		t.Fatal("test form must exceed the default size limit")
	}
	args := map[string]any{"repo": "repo-a", "actions": actions}
	first, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_repository", Arguments: args})
	if err != nil || first.IsError || len(first.InputRequests) != 1 || writes != 0 {
		t.Fatalf("initial batch approval = %+v, %v, writes = %d", first, err, writes)
	}
	prompt := first.InputRequests[secretsInputID].(*mcp.ElicitParams)
	if status := f.pages.submit(prompt.URL, values); status != http.StatusSeeOther {
		t.Fatalf("large form submission returned %d", status)
	}
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_repository", Arguments: args, RequestState: first.RequestState,
		InputResponses: mcp.InputResponseMap{secretsInputID: &mcp.ElicitResult{Action: "accept"}},
	})
	if err != nil || res.IsError || writes != 100 {
		t.Fatalf("batch did not complete 100 webhook writes: %+v, %v, writes = %d", res, err, writes)
	}
}

func TestManageRepositoryBatchRejectsInvalidOrDeniedInputsBeforeRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   map[string]any
		policy Policy
		want   string
	}{
		{"malformed later action", map[string]any{"repo": "repo-a", "actions": []any{batchRule("force"), batchRule("")}}, Policy{}, "action 2"},
		{"duplicate repo", map[string]any{"repos": []string{"repo-a", "REPO-A"}}, Policy{}, "duplicate repository"},
		{"both repo fields", map[string]any{"repo": "repo-a", "repos": []string{"repo-b"}}, Policy{}, "either repo or"},
		{"empty actions", map[string]any{"repo": "repo-a", "actions": []any{}}, Policy{}, "nonempty actions"},
		{"empty repo", map[string]any{"repos": []string{"repo-a", ""}}, Policy{}, "repository slug"},
		{"too many operations", map[string]any{
			"repo": "repo-a", "actions": slices.Repeat([]map[string]any{batchRule("force")}, 101),
		}, Policy{}, "at most 100"},
		{"tool denied", map[string]any{"repos": []string{"repo-a", "repo-b"}}, Policy{Tools: []string{"*", "!manage_repository@repo-b"}}, "denied"},
		{"repo denied", map[string]any{"repos": []string{"repo-a", "repo-b"}}, Policy{Repositories: []string{"repo-a"}}, "not allowed"},
		{"rename multiple", map[string]any{"repos": []string{"repo-a", "repo-b"}, "slug": "new-name"}, Policy{}, "exactly one repository"},
		{"move before subsequent action", map[string]any{"repo": "repo-a", "actions": []any{map[string]any{"project": "APP"}, batchRule("force")}}, Policy{}, "last action"},
		{"repeat change", map[string]any{"repo": "repo-a", "actions": []any{
			map[string]any{"branch_restriction": map[string]any{"action": "delete", "id": 42}},
			map[string]any{"branch_restriction": map[string]any{"action": "delete", "id": 42}},
		}}, Policy{}, "only be changed once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := connectUUID(t, tc.policy, func(http.ResponseWriter, *http.Request) {
				t.Error("invalid or denied batch reached Bitbucket")
			})
			out, failed := call(t, cs, "manage_repository", tc.args)
			if !failed || !strings.Contains(out, tc.want) {
				t.Fatalf("result = %s (error %v), want %q", out, failed, tc.want)
			}
		})
	}
}

func TestManageRepositoryBatchReadsBoundedResults(t *testing.T) {
	desc := strings.Repeat("x", maxRepositoryResultBytes/2)
	cs := connectUUID(t, Policy{}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("read batch sent %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case batchRepoPath + "repo-a":
			writeJSON(w, map[string]string{"uuid": fakeSourceUUID, "slug": "repo-a", "description": desc})
		case batchRepoPath + "repo-b":
			writeJSON(w, map[string]string{"uuid": fakeTargetUUID, "slug": "repo-b", "description": desc})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	out := decodeResult[manageRepositoryResult](t, mustCall(t, cs, "manage_repository", map[string]any{"repos": []string{"repo-a", "repo-b"}}))
	if out.Status != "completed" || len(out.Results) != 2 || out.Results[0].Result == nil ||
		out.Results[0].Result.Repository == nil ||
		out.Results[0].Result.Repository.Slug != "repo-a" || out.Results[1].Repo != "repo-b" ||
		out.Results[1].Status != "completed" || !out.Results[1].ResultOmitted || out.Results[1].Result != nil {
		t.Fatalf("batch result did not preserve statuses at the size limit")
	}
}

func TestManageRepositoryBatchPinsReadsAfterEarlierWrite(t *testing.T) {
	for _, name := range []string{"same repository", "reassigned slug"} {
		t.Run(name, func(t *testing.T) {
			written := false
			pinnedReads := 0
			cs := connectUUID(t, Policy{Repositories: []string{"repo-a"}, ConfirmationTools: []string{"delete_variable"}},
				func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodGet && r.URL.Path == batchRepoPath+"repo-a":
						uuid := fakeSourceUUID
						if written && name == "reassigned slug" {
							uuid = fakeTargetUUID
						}
						writeJSON(w, map[string]string{"uuid": uuid, "slug": "repo-a"})
					case r.Method == http.MethodPost && r.URL.Path == batchRepoPath+fakeSourceUUID+"/branch-restrictions":
						written = true
						writeJSON(w, bitbucket.BranchRule{ID: 42})
					case r.Method == http.MethodGet && r.URL.Path == batchRepoPath+fakeSourceUUID+"/hooks":
						pinnedReads++
						writeJSON(w, map[string]any{"values": []bitbucket.Webhook{{Description: "approved webhook"}}})
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
					}
				})
			result, failed := call(t, cs, "manage_repository", map[string]any{
				"repo": "repo-a", "actions": []any{batchRule("force"), map[string]any{"webhook": map[string]string{"action": "list"}}},
			})
			out := decodeResult[manageRepositoryResult](t, result)
			if !written || len(out.Results) != 2 || out.Results[0].Status != "completed" {
				t.Fatalf("earlier write did not complete: %s", result)
			}
			if name == "reassigned slug" {
				if !failed || out.Status != "partial" || out.Results[1].Result != nil || pinnedReads != 0 ||
					!strings.Contains(out.Results[1].Error, "repository identity or project changed") {
					t.Fatalf("reassigned target was not rejected: %s", result)
				}
			} else if failed || out.Status != "completed" || pinnedReads != 1 || !strings.Contains(result, "approved webhook") {
				t.Fatalf("approved target was not read by UUID: %s", result)
			}
		})
	}
}

func TestManageRepositoryBatchBoundsExpandedApproval(t *testing.T) {
	f := newFake()
	cs := connectRetryingItself(t, f)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !serveBatchRepo(w, r) {
			t.Errorf("oversized approval sent %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	f.tools.bb = bitbucket.New(&http.Client{Transport: handlerTransport{handler}}, bitbucket.Config{Workspace: "ws"})
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_repository", Arguments: map[string]any{
		"repos": []string{"repo-a", "repo-b"}, "description": strings.Repeat("x", maxRepositoryApprovalBytes/2),
	}})
	if err != nil || !res.IsError || len(res.InputRequests) != 0 ||
		!strings.Contains(res.Content[0].(*mcp.TextContent).Text, "batch approval exceeds 512 KiB") {
		t.Fatal("oversized approval was not rejected before opening a page")
	}
}

func TestManageRepositoryBatchPreflightsProjectsBeforeAnyWrite(t *testing.T) {
	policy := Policy{Projects: []string{"APP"}, ConfirmationTools: []string{"delete_variable"}}
	cs := connectUUID(t, policy, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("batch wrote before all projects were approved: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case batchRepoPath + "repo-a":
			writeJSON(w, map[string]any{"uuid": fakeSourceUUID, "slug": "repo-a", "project": map[string]string{"key": "APP"}})
		case batchRepoPath + "repo-b":
			writeJSON(w, map[string]any{"uuid": fakeTargetUUID, "slug": "repo-b", "project": map[string]string{"key": "OPS"}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	args := batchRule("force")
	args["repos"] = []string{"repo-a", "repo-b"}
	out, failed := call(t, cs, "manage_repository", args)
	if !failed || !strings.Contains(out, "repo-b") || !strings.Contains(out, "project is not allowed") {
		t.Fatalf("batch project authorization result: %s, error %v", out, failed)
	}
}

func TestManageRepositoryBatchReturnsPartialResultsAndSkipsLaterActions(t *testing.T) {
	var requests []string
	cs := connectUUID(t, Policy{ConfirmationTools: []string{"delete_variable"}}, func(w http.ResponseWriter, r *http.Request) {
		if serveBatchRepo(w, r) {
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != batchRepoPath+fakeSourceUUID+"/branch-restrictions" {
			t.Errorf("unexpected request after write failure: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var rule bitbucket.BranchRuleFields
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			t.Error(err)
		}
		requests = append(requests, rule.Kind)
		if rule.Kind == "delete" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeJSON(w, bitbucket.BranchRule{ID: 42, BranchRuleFields: rule})
	})
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_repository", Arguments: map[string]any{
		"repos": []string{"repo-a", "repo-b"}, "actions": []any{batchRule("force"), batchRule("delete")},
	}})
	if err != nil || !res.IsError || res.StructuredContent == nil {
		t.Fatalf("missing structured partial result: %+v, %v", res, err)
	}
	out := decodeResult[manageRepositoryResult](t, res.Content[0].(*mcp.TextContent).Text)
	if out.Status != "partial" || len(out.Results) != 4 || out.Results[0].Result == nil ||
		out.Results[0].Result.BranchRestriction == nil || out.Results[0].Result.BranchRestriction.ID != 42 ||
		!strings.Contains(out.Results[1].Error, "403") || !slices.Equal(requests, []string{"force", "delete"}) {
		t.Fatalf("partial result = %+v, attempts = %v", out, requests)
	}
	for i, want := range []string{"completed", "failed", "skipped", "skipped"} {
		if out.Results[i].Status != want {
			t.Errorf("entry %d status = %q, want %q", i, out.Results[i].Status, want)
		}
	}
}

func TestManageRepositoryBatchRechecksAllIdentitiesAfterBrowserSubmission(t *testing.T) {
	f := newFake()
	cs := connectRetryingItself(t, f)
	checks := atomic.Int32{}
	changed := atomic.Bool{}
	retryReady := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == batchRepoPath+"repo-b" {
			uuid := fakeTargetUUID
			if changed.Load() {
				uuid = "{bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbc}"
			}
			writeJSON(w, map[string]string{"uuid": uuid, "slug": "repo-b"})
			if checks.Add(1) == 2 {
				close(retryReady)
			}
			return
		}
		if !serveBatchRepo(w, r) {
			t.Errorf("unexpected request after repository identity changed: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	f.tools.bb = bitbucket.New(&http.Client{Transport: handlerTransport{handler}}, bitbucket.Config{Workspace: "ws"})
	args := batchRule("force")
	args["repos"] = []string{"repo-a", "repo-b"}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "manage_repository", Arguments: args})
	if err != nil || first.IsError || len(first.InputRequests) != 1 {
		t.Fatalf("initial approval = %+v, %v", first, err)
	}
	type response struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan response, 1)
	go func() {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "manage_repository", Arguments: args, RequestState: first.RequestState,
			InputResponses: mcp.InputResponseMap{secretsInputID: &mcp.ElicitResult{Action: "accept"}},
		})
		done <- response{res, err}
	}()
	select {
	case <-retryReady:
	case <-ctx.Done():
		t.Fatal("retry did not read repository settings")
	}
	changed.Store(true)
	prompt := first.InputRequests[secretsInputID].(*mcp.ElicitParams)
	if status := f.pages.submit(prompt.URL, nil); status != http.StatusSeeOther {
		t.Fatalf("form submission returned %d", status)
	}
	retry := <-done
	if retry.err != nil || !retry.result.IsError || !strings.Contains(retry.result.Content[0].(*mcp.TextContent).Text, "changed while awaiting approval") {
		t.Fatalf("changed batch identity was not rejected: %+v, %v", retry.result, retry.err)
	}
	expectPagesClosed(t, f, 1)
}
