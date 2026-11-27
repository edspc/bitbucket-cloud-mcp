package tools

import (
	"encoding/json"
	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestRepositoryWebhookOperations(t *testing.T) {
	const repoUUID = "{01234567-89ab-cdef-0123-456789abcdef}"
	const hookUUID = "{abcdef01-2345-6789-abcd-ef0123456789}"
	const repo = "/2.0/repositories/ws/repo-a"
	const pinned = "/2.0/repositories/ws/" + repoUUID + "/hooks"
	hook := map[string]any{
		"uuid": hookUUID, "url": "https://user:password@example.com/webhook?token=hidden",
		"active": true, "events": []string{"repo:push"}, "secret_set": true, "secret": "never-return-this",
	}
	var writes []string
	cs := connectUUID(t, Policy{ConfirmationTools: []string{"delete_variable"}, Repositories: []string{"repo-a"}, Projects: []string{}}, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == http.MethodGet {
			switch path {
			case repo:
				writeJSON(w, map[string]string{"uuid": repoUUID, "slug": "repo-a"})
			case repo + "/hooks":
				if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("pagelen") != "1" {
					t.Errorf("bad pagination query: %s", r.URL.RawQuery)
				}
				writeJSON(w, map[string]any{"values": []any{hook}, "next": "https://other.invalid/page/3"})
			case repo + "/hooks/" + hookUUID:
				writeJSON(w, hook)
			default:
				t.Errorf("unexpected GET %s", path)
			}
			return
		}
		var got, want map[string]any
		switch {
		case r.Method == http.MethodPost && path == pinned:
			want = map[string]any{"url": "https://example.com/webhook", "description": "CI", "active": true, "events": []any{"repo:push"}}
		case r.Method == http.MethodPut && path == pinned+"/"+hookUUID:
			want = map[string]any{"active": false, "description": "", "secret": ""}
		case r.Method == http.MethodDelete && path == pinned+"/"+hookUUID:
			writes = append(writes, r.Method)
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			t.Errorf("unexpected write %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s payload = %#v, want %#v, error = %v", r.Method, got, want, err)
		}
		writes = append(writes, r.Method)
		writeJSON(w, hook)
	})
	run := func(tool string, args map[string]any) manageRepositoryResult {
		t.Helper()
		result := mustCall(t, cs, tool, map[string]any{"repo": "repo-a", "webhook": args})
		for _, secret := range []string{"password", "hidden", "never-return-this"} {
			if strings.Contains(result, secret) {
				t.Fatal("secret leaked into webhook result")
			}
		}
		return decodeResult[manageRepositoryResult](t, result)
	}
	out := run("get_repository", map[string]any{"page": 2, "limit": 1})
	if out.Webhooks == nil || len(*out.Webhooks) != 1 || out.NextPage != 3 || (*out.Webhooks)[0].URL != "https://example.com/webhook" {
		t.Fatalf("unexpected webhook page: %+v", out)
	}
	out = run("get_repository", map[string]any{"uuid": hookUUID})
	if out.Webhook == nil || out.Webhook.UUID != hookUUID || !out.Webhook.SecretSet {
		t.Fatalf("unexpected webhook: %+v", out)
	}
	run("manage_repository", map[string]any{"action": "create", "settings": map[string]any{
		"url": "https://example.com/webhook", "description": "CI", "active": true, "events": []string{"repo:push"},
	}})
	run("manage_repository", map[string]any{"action": "update", "uuid": hookUUID, "remove_secret": true, "settings": map[string]any{
		"active": false, "description": "",
	}})
	out = run("manage_repository", map[string]any{"action": "delete", "uuid": hookUUID})
	if out.DeletedWebhookUUID != hookUUID || !reflect.DeepEqual(writes, []string{"POST", "PUT", "DELETE"}) {
		t.Fatalf("unexpected delete result: %+v, writes = %v", out, writes)
	}
}

func TestWebhookSigningSecretRequiresBrowser(t *testing.T) {
	cs := connectUUID(t, Policy{ConfirmationTools: []string{"delete_variable"}}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("webhook write reached Bitbucket before secret entry")
		}
		writeJSON(w, map[string]string{"uuid": "{01234567-89ab-cdef-0123-456789abcdef}", "slug": "repo-a"})
	})
	out, failed := call(t, cs, "manage_repository", map[string]any{"repo": "repo-a", "webhook": map[string]any{
		"action": "create", "set_secret": true,
		"settings": map[string]any{"url": "https://example.com/hooks", "description": "CI", "active": false, "events": []string{"repo:push"}},
	}})
	if !failed || !strings.Contains(out, "URL elicitation") {
		t.Fatalf("secret entry was bypassed: %s", out)
	}
}

func TestReadOnlyWebhookRespectsRepositoryPolicy(t *testing.T) {
	cs := connectUUID(t, Policy{ReadOnly: true, Repositories: []string{"repo-b"}}, func(http.ResponseWriter, *http.Request) {
		t.Error("repository denied by policy reached Bitbucket")
	})
	_, failed := call(t, cs, "get_repository", map[string]any{"repo": "repo-a", "webhook": map[string]any{}})
	if !failed {
		t.Fatal("webhook read bypassed repository policy")
	}
}

func TestWebhookApprovalRejectsChangedHiddenDestination(t *testing.T) {
	const repo = "/2.0/repositories/ws/repo-a"
	const hookUUID = "{abcdef01-2345-6789-abcd-ef0123456789}"
	f := newFake()
	cs := connectRetryingItself(t, f)
	destination := "https://example.com/hook?route=hidden-before"
	f.tools.bb = bitbucket.New(&http.Client{Transport: handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == repo:
			writeJSON(w, map[string]string{"uuid": "{01234567-89ab-cdef-0123-456789abcdef}", "slug": "repo-a"})
		case r.Method == http.MethodGet && r.URL.Path == repo+"/hooks/"+hookUUID:
			writeJSON(w, map[string]any{"uuid": hookUUID, "url": destination, "active": false})
		default:
			t.Errorf("unexpected request after destination changed: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})}}, bitbucket.Config{Workspace: "ws"})
	args := map[string]any{"repo": "repo-a", "webhook": map[string]any{
		"action": "update", "uuid": hookUUID, "settings": map[string]any{"active": true},
	}}
	first, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_repository", Arguments: args})
	if err != nil || first.IsError {
		t.Fatalf("initial approval: %+v, %v", first, err)
	}
	if strings.Contains(f.pages.openedPage(), "hidden-before") {
		t.Fatal("hidden destination leaked into approval")
	}
	destination = "https://example.com/hook?route=hidden-after"
	prompt := first.InputRequests[secretsInputID].(*mcp.ElicitParams)
	if status := f.pages.submit(prompt.URL, nil); status != http.StatusSeeOther {
		t.Fatal(status)
	}
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "manage_repository", Arguments: args,
		RequestState:   first.RequestState,
		InputResponses: mcp.InputResponseMap{secretsInputID: &mcp.ElicitResult{Action: "accept"}},
	})
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "webhook changed") {
		t.Fatalf("changed destination was not rejected: %+v, %v", result, err)
	}
}
