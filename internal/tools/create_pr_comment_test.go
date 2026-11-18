package tools

import (
	"encoding/json"
	"fmt"
	"html"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func commentArgs(parent int) map[string]any {
	return map[string]any{"repo": "repo-a", "pull_request": 7, "content": "  Fixed **this**.\n<script>text</script>\n", "parent_id": parent}
}

func TestCreateCommentAndReplyWithBrowserApproval(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, version string) {
		for _, parent := range []int{0, 11} {
			t.Run(fmt.Sprint(parent), func(t *testing.T) {
				f := approvalFake()
				// A reply to an inline comment inherits the existing thread.
				p := f.prComments["repo-a/7"][11]
				p.Inline = &bitbucket.CommentInline{Path: "app.go"}
				f.prComments["repo-a/7"][11] = p
				u := &fakeUser{pages: f.pages, action: "accept", values: url.Values{}}
				cs := connectClient(t, newTestServer(t, f), clientOptions(u.elicit), version)
				args := commentArgs(parent)
				out := mustCall(t, cs, "create_pull_request_comment", args)
				var result createPullRequestCommentResult
				if err := json.Unmarshal([]byte(out), &result); err != nil || result.Status != "created" || result.CommentID != 102 || result.ParentID != parent || result.URL != "https://bitbucket.org/ws/repo-a/pull-requests/7#comment-102" {
					t.Fatalf("result = %s, %v", out, err)
				}
				if len(f.mutationList()) != 1 || len(f.commentBodies) != 1 {
					t.Fatal("write was not performed exactly once")
				}
				var posted bitbucket.PullRequestComment
				if err := json.Unmarshal([]byte(f.commentBodies[0]), &posted); err != nil || posted.Content.Raw != args["content"] || (posted.Parent != nil) != (parent > 0) {
					t.Fatalf("wrong comment posted: %+v, %v", posted, err)
				}
				body := html.UnescapeString(f.pages.openedPage())
				for _, want := range []string{"repo-a", `"pull_request": 7`, "Fixed **this**.", "\\u003cscript\\u003e", "https://bitbucket.org/ws/repo-a/pull-requests/7"} {
					if !strings.Contains(body, want) {
						t.Fatalf("approval hides %q", want)
					}
				}
				if parent > 0 && !strings.Contains(body, "#comment-11") {
					t.Fatal("approval hides the parent URL")
				}
			})
		}
	})
}

func TestCreateCommentRejectsInputAndTargetsBeforeApproval(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*fakeBitbucket, map[string]any)
	}{
		{"blank content", func(_ *fakeBitbucket, a map[string]any) { a["content"] = " \n" }},
		{"too long", func(_ *fakeBitbucket, a map[string]any) {
			a["content"] = strings.Repeat("x", bitbucket.MaxCommentBytes+1)
		}},
		{"negative parent", func(_ *fakeBitbucket, a map[string]any) { a["parent_id"] = -1 }},
		{"invalid PR", func(_ *fakeBitbucket, a map[string]any) { a["pull_request"] = 0 }},
		{"missing PR", func(_ *fakeBitbucket, a map[string]any) { a["pull_request"] = 8 }},
		{"foreign parent", func(_ *fakeBitbucket, a map[string]any) { a["parent_id"] = 99 }},
		{"deleted parent", func(f *fakeBitbucket, _ map[string]any) {
			p := f.prComments["repo-a/7"][11]
			p.Deleted = true
			f.prComments["repo-a/7"][11] = p
		}},
		{"pending parent", func(f *fakeBitbucket, _ map[string]any) {
			p := f.prComments["repo-a/7"][11]
			p.Pending = true
			f.prComments["repo-a/7"][11] = p
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := approvalFake()
			a := commentArgs(11)
			tc.change(f, a)
			_, isErr := call(t, connectApproving(t, f), "create_pull_request_comment", a)
			if !isErr || f.pages.served() || len(f.mutationList()) != 0 {
				t.Fatal("invalid comment reached approval or write")
			}
		})
	}
}

func TestReplyRechecksParentAfterBrowserWait(t *testing.T) {
	for _, change := range []string{"edit", "delete", "remove"} {
		t.Run(change, func(t *testing.T) {
			f := approvalFake()
			cs := connectRetryingItself(t, f)
			args := commentArgs(11)
			first, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_pull_request_comment", Arguments: args})
			if err != nil || first.IsError {
				t.Fatalf("initial: %+v, %v", first, err)
			}
			done := retryWaitingForBrowser(t, cs, "create_pull_request_comment", args, first)
			f.mu.Lock()
			p := f.prComments["repo-a/7"][11]
			if change == "edit" {
				p.Content.Raw = "changed"
			} else {
				p.Deleted = true
			}
			f.prComments["repo-a/7"][11] = p
			if change == "remove" {
				delete(f.prComments["repo-a/7"], 11)
			}
			f.mu.Unlock()
			if status := f.pages.submit(first.InputRequests[secretsInputID].(*mcp.ElicitParams).URL, nil); status != http.StatusSeeOther {
				t.Fatal(status)
			}
			select {
			case res := <-done:
				if !res.IsError || len(f.mutationList()) != 0 {
					t.Fatalf("changed parent accepted: %+v", res)
				}
			case <-time.After(time.Second):
				t.Fatal("retry did not finish")
			}
		})
	}
}

func TestCommentApprovalCannotChangeContentOrParentOrBeReplayed(t *testing.T) {
	for _, field := range []string{"content", "parent_id", "unchanged"} {
		t.Run(field, func(t *testing.T) {
			f := approvalFake()
			cs := connectRetryingItself(t, f)
			args := commentArgs(0)
			first, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_pull_request_comment", Arguments: args})
			if err != nil || first.IsError {
				t.Fatalf("initial: %+v, %v", first, err)
			}
			if status := f.pages.submit(first.InputRequests[secretsInputID].(*mcp.ElicitParams).URL, nil); status != http.StatusSeeOther {
				t.Fatal(status)
			}
			retry := maps.Clone(args)
			if field == "content" {
				retry[field] = "different text"
			}
			if field == "parent_id" {
				retry[field] = 11
			}
			params := &mcp.CallToolParams{Name: "create_pull_request_comment", Arguments: retry, RequestState: first.RequestState, InputResponses: mcp.InputResponseMap{secretsInputID: &mcp.ElicitResult{Action: "accept"}}}
			res, err := cs.CallTool(t.Context(), params)
			wantWrites := 0
			if field == "unchanged" {
				wantWrites = 1
			}
			if err != nil || res.IsError != (field != "unchanged") || len(f.mutationList()) != wantWrites {
				t.Fatalf("argument binding failed: %+v, %v", res, err)
			}
			res, err = cs.CallTool(t.Context(), params)
			if err != nil || !res.IsError || len(f.mutationList()) != wantWrites {
				t.Fatal("approval replayed")
			}
		})
	}
}

func TestCreateCommentPolicyAndConfirmationOverride(t *testing.T) {
	for _, tc := range []struct {
		name    string
		policy  Policy
		allowed bool
	}{
		{"tool denied", Policy{Tools: []string{"list_pull_request_comments"}}, false},
		{"repo denied", Policy{Repositories: []string{"repo-b"}}, false},
		{"repo tool denied", Policy{Tools: []string{"*", "!create_pull_request_comment@repo-a"}}, false},
		{"project denied", Policy{Projects: []string{"OPS"}}, false},
		{"project allowed", Policy{Projects: []string{"APP"}}, true},
		{"explicit repo union", Policy{Projects: []string{"OPS"}, Repositories: []string{"repo-a"}}, true},
		{"repo tool allowed", Policy{Tools: []string{"create_pull_request_comment@repo-a"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := approvalFake()
			f.projects = map[string]string{"repo-a": "APP"}
			// Explicit operator policy allows this write without a browser.
			tc.policy.ConfirmationTools = []string{"run_pipeline"}
			cs := connectClient(t, newTestServer(t, f, tc.policy), clientOptions(nil), "")
			out, isErr := call(t, cs, "create_pull_request_comment", commentArgs(0))
			if isErr == tc.allowed || f.pages.served() || len(f.mutationList()) != map[bool]int{true: 1, false: 0}[tc.allowed] {
				t.Fatalf("policy: %s", out)
			}
			if !tc.allowed && len(f.prRequests) != 0 {
				t.Fatal("denied call read PR data")
			}
		})
	}
}

func TestCommentAmbiguousWriteDoesNotRetry(t *testing.T) {
	for _, response := range []string{"malformed", "missing ID", "API error"} {
		t.Run(response, func(t *testing.T) {
			f := approvalFake()
			cs := connectApproving(t, f)
			upstream := f.handler(t)
			posts := 0
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments") {
					posts++
					switch response {
					case "malformed":
						_, _ = fmt.Fprint(w, `{"id":`)
					case "missing ID":
						_, _ = fmt.Fprint(w, `{}`)
					default:
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = fmt.Fprint(w, `{"error":{"message":"try later"}}`)
					}
					return
				}
				upstream.ServeHTTP(w, r)
			})
			f.tools.bb = bitbucket.New(&http.Client{Transport: handlerTransport{h}}, bitbucket.Config{Workspace: "ws"})
			out, isErr := call(t, cs, "create_pull_request_comment", commentArgs(0))
			if !isErr || posts != 1 || !strings.Contains(out, "inspect the PR before retrying") {
				t.Fatalf("ambiguous write retried or unclear: %s, posts=%d", out, posts)
			}
		})
	}
}
