package tools

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

func TestDeploymentVariablesRejectMissingEnvironmentUUID(t *testing.T) {
	for _, operation := range []string{"list", "create plain", "update plain", "create secured", "update secured", "delete", "link token", "replace token"} {
		t.Run(operation, func(t *testing.T) {
			f := newFake()
			original := []bitbucket.Variable{{UUID: "{keep}", Key: "KEEP", Value: "repository-value"}}
			f.vars["repo-b"] = slices.Clone(original)
			user := &fakeUser{pages: f.pages, action: "accept", values: url.Values{"SECRET": {"fake-secret"}, "KEEP": {"fake-secret"}, tokenField: {f.validToken}}}
			cs := connect(t, f, user.elicit)
			upstream := f.handler(t)
			f.tools.bb = bitbucket.New(&http.Client{Transport: handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/2.0/repositories/ws/repo-b/environments" {
					writeJSON(w, map[string]any{"values": []map[string]string{{"name": "Production"}}})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/2.0/repositories/ws/repo-b/pipelines_config/variables") {
					t.Error("deployment operation reached repository variables")
				}
				upstream.ServeHTTP(w, r)
			})}}, bitbucket.Config{Workspace: "ws"})
			name := "set_variable"
			args := map[string]any{"scope": "deployment", "repo": "repo-b", "environment": "Production", "name": "NEW", "value": "plain"}
			switch operation {
			case "list":
				name = "list_variables"
				delete(args, "name")
				delete(args, "value")
			case "update plain":
				args["name"], args["force"] = "KEEP", true
			case "create secured", "update secured":
				delete(args, "value")
				args["name"], args["secured"] = "SECRET", true
				if operation == "update secured" {
					args["name"], args["force"] = "KEEP", true
				}
			case "delete":
				name = "delete_variable"
				delete(args, "value")
				args["name"] = "KEEP"
			case "link token", "replace token":
				name, args = "link_repos_by_token", tokenArgs("deployment", "Production", operation == "replace token")
				if operation == "replace token" {
					args["variable_name"] = "KEEP"
				}
			}
			out, failed := call(t, cs, name, args)
			if !failed || !strings.Contains(out, "no deployment environment UUID") {
				t.Fatalf("missing UUID was not rejected: %s", out)
			}
			if f.pages.served() {
				t.Error("missing UUID must be rejected before asking for approval or secrets")
			}
			if len(f.mutationList()) != 0 || !slices.Equal(f.varsOf("repo-b"), original) {
				t.Fatal("missing UUID changed repository variables")
			}
		})
	}
}
