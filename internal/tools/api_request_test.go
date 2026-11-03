package tools

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

func TestAPIProxyNotRegisteredByDefault(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cs := connectUUID(t, Policy{APIProxyEnabled: enabled}, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		})
		list, err := cs.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tool := range list.Tools {
			if tool.Name == "api_request" {
				found = true
				if tool.Annotations == nil || tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
					t.Fatal("proxy annotations promise read-only behavior")
				}
			}
		}
		if found != enabled {
			t.Fatalf("proxy registered %v, enabled %v", found, enabled)
		}
		if !enabled {
			if _, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "api_request", Arguments: map[string]any{"method": "GET", "path": "/user"}}); err == nil {
				t.Fatal("disabled proxy could be called by name")
			}
		} else {
			out := mustCall(t, cs, "api_request", map[string]any{"method": "GET", "path": "/user"})
			if !strings.Contains(out, `"content_trust":"untrusted"`) || !strings.Contains(out, `"status":200`) {
				t.Fatal(out)
			}
		}
	}
}

func TestAPIProxyHonorsRestrictionsBeforeAnyRequest(t *testing.T) {
	for _, policy := range []Policy{
		{Repositories: []string{}}, {Repositories: []string{"repo-a"}}, {Projects: []string{}},
		{Projects: []string{"APP"}}, {Refs: []string{}}, {Refs: []string{"branch:main"}}, {Tools: []string{"get_file"}},
	} {
		policy.APIProxyEnabled = true
		cs := connectUUID(t, policy, func(http.ResponseWriter, *http.Request) { t.Error("restricted proxy reached API") })
		out, failed := call(t, cs, "api_request", map[string]any{"method": "GET", "path": "/repositories/ws/repo-a/issues"})
		if !failed || !strings.Contains(out, "policy") && !strings.Contains(out, "limits") {
			t.Fatalf("restriction bypassed: %s", out)
		}
	}
}

func TestAPIProxyWritesRequireBrowserApproval(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, version string) {
		for _, action := range []string{"accept", "decline", "unsupported"} {
			f := newFake()
			s := newTestServer(t, f, Policy{APIProxyEnabled: true})
			requests := 0
			f.tools.bb = bitbucket.New(&http.Client{Transport: handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "POST" || r.URL.Path != "/2.0/repositories/ws/repo-a/issues" {
					t.Fatal(r.URL)
				}
				_, _ = fmt.Fprint(w, `{"id":42}`)
			})}}, bitbucket.Config{Workspace: "ws", Email: "dev@example.com", Token: "api-secret"})
			u := &fakeUser{pages: f.pages, action: action, values: url.Values{}}
			opts := clientOptions(u.elicit)
			if action == "unsupported" {
				opts = clientOptions(nil)
			}
			cs := connectClient(t, s, opts, version)
			out, failed := call(t, cs, "api_request", map[string]any{"method": "POST", "path": "/repositories/ws/repo-a/issues", "body": map[string]any{"title": "requested issue"}})
			if action == "accept" {
				if failed || requests != 1 || !strings.Contains(f.pages.openedPage(), "requested issue") || !strings.Contains(f.pages.openedPage(), "POST") {
					t.Fatalf("approved request failed: %s, requests %d", out, requests)
				}
			} else if !failed || requests != 0 {
				t.Fatalf("unapproved request sent: %s, requests %d", out, requests)
			}
		}
	})
}

func TestAPIProxyConfirmationConfigurationAndValidation(t *testing.T) {
	for _, tc := range []struct {
		method  string
		confirm []string
		fails   bool
	}{
		{"GET", nil, false}, {"HEAD", nil, false}, {"OPTIONS", nil, false},
		{"POST", nil, true}, {"PUT", nil, true}, {"PATCH", nil, true}, {"DELETE", nil, true},
		{"GET", []string{"api_request"}, true}, {"POST", []string{"run_pipeline"}, false},
	} {
		requests := 0
		cs := connectUUID(t, Policy{APIProxyEnabled: true, ConfirmationTools: tc.confirm}, func(w http.ResponseWriter, _ *http.Request) {
			requests++
			w.WriteHeader(http.StatusNoContent)
		})
		out, failed := call(t, cs, "api_request", map[string]any{"method": tc.method, "path": "/user"})
		if failed != tc.fails || (requests == 0) != tc.fails {
			t.Fatalf("%s confirm %v: %s requests %d", tc.method, tc.confirm, out, requests)
		}
	}
	f := newFake()
	u := &fakeUser{pages: f.pages, action: "accept", values: url.Values{}}
	cs := connectClient(t, newTestServer(t, f, Policy{APIProxyEnabled: true}), clientOptions(u.elicit), "")
	if out, failed := call(t, cs, "api_request", map[string]any{"method": "POST", "path": "/user", "body": map[string]any{"password": "secret-marker"}}); !failed || f.pages.served() {
		t.Fatalf("invalid secret-bearing request reached approval: %s", out)
	}
}
