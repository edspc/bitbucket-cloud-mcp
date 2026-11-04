package tools

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

func TestListCommitsFiltersPagesAndPinnedRevision(t *testing.T) {
	for _, mode := range []string{"path", "prefix", "follow_renames", "all_refs"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			cursor := ""
			cs := connectUUID(t, Policy{}, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if strings.Contains(r.URL.Path, "/commit/") {
					writeJSON(w, bitbucket.Commit{Hash: testPipelineCommit})
					return
				}
				if mode != "all_refs" && !strings.Contains(r.URL.Path, testPipelineCommit) {
					t.Fatal("history was not pinned to the resolved commit")
				}
				if mode == "prefix" && r.URL.Query().Get("path") != "src/pay*" || mode == "path" && r.URL.Query().Get("path") != "src/pay" {
					t.Fatal(r.URL)
				}
				last := r.URL.Query().Has("page")
				if last && r.URL.String() != cursor {
					t.Fatal("opaque cursor was changed")
				}
				hash, historicalPath := "first", "old.go"
				payload := map[string]any{"size": 2}
				if last {
					hash, historicalPath = "last", "new.go"
				} else {
					next := *r.URL
					q := next.Query()
					q.Set("page", "bookmark/opaque+=")
					q.Set("ctx", "preserved iterator state")
					next.RawQuery = q.Encode()
					cursor = next.String()
					payload["next"] = cursor
				}
				if mode == "follow_renames" {
					if !strings.Contains(r.URL.Path, "/filehistory/") || r.URL.Query().Get("renames") != "true" {
						t.Fatal(r.URL)
					}
					payload["values"] = []bitbucket.HistoryEntry{{Path: historicalPath, Commit: bitbucket.HistoryCommit{Hash: hash}}}
				} else {
					payload["values"] = []bitbucket.HistoryCommit{{Hash: hash}}
				}
				writeJSON(w, payload)
			})
			args := map[string]any{"repo": "repo-a", "limit": 1}
			if mode != "all_refs" {
				args["revision"] = "release/next"
			}
			if mode == "prefix" {
				args["prefix"] = "src/pay"
			} else if mode != "all_refs" {
				args["path"] = "src/pay"
			}
			if mode == "follow_renames" {
				args["follow_renames"] = true
			}
			first := decodeResult[listCommitsResult](t, mustCall(t, cs, "list_commits", args))
			if first.NextCursor == "" || first.NextCursor != cursor || first.ContentTrust != "untrusted" || len(first.Commits) != 1 || first.Commits[0].Commit.Hash != "first" {
				t.Fatalf("first page: %+v", first)
			}
			if first.Revision != "" {
				args["revision"] = first.Revision
			}
			args["cursor"] = first.NextCursor
			last := decodeResult[listCommitsResult](t, mustCall(t, cs, "list_commits", args))
			if last.NextCursor != "" || len(last.Commits) != 1 || last.Commits[0].Commit.Hash != "last" {
				t.Fatalf("last page: %+v", last)
			}
			want := 3
			if mode == "all_refs" {
				want = 2
			}
			if calls != want {
				t.Fatalf("requests = %d, want %d", calls, want)
			}
		})
	}
}

func TestHistoryRejectsInvalidArgumentsBeforeAPI(t *testing.T) {
	for _, extra := range []map[string]any{
		{"path": "file", "prefix": "pre"}, {"follow_renames": true}, {"follow_renames": true, "path": "file"},
		{"follow_renames": true, "path": "src/", "revision": "main"}, {"prefix": "src/*"}, {"path": "../file"},
		{"path": "/"}, {"prefix": "/"}, {"path": "src//file"}, {"revision": "../main"},
		{"revision": "main", "cursor": "opaque"}, {"cursor": "not-a-url"}, {"limit": 51},
	} {
		cs := connectUUID(t, Policy{}, func(http.ResponseWriter, *http.Request) { t.Error("invalid input reached API") })
		args := map[string]any{"repo": "repo-a"}
		for key, value := range extra {
			args[key] = value
		}
		if out, failed := call(t, cs, "list_commits", args); !failed {
			t.Fatalf("accepted %v: %s", args, out)
		}
	}
}

func TestHistoryPreservesPoliciesAndLiveProjectChecks(t *testing.T) {
	for _, policy := range []Policy{{Repositories: []string{}}, {Projects: []string{}}, {Projects: []string{"OTHER"}}, {Tools: []string{"get_uuid"}}} {
		cs := connectUUID(t, policy, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("fields") != "project.key,project.uuid" {
				t.Error("denied request read history")
			}
			writeJSON(w, map[string]any{"project": map[string]string{"key": "APP"}})
		})
		cursor := bitbucket.APIURL + "/repositories/ws/repo-a/commits?pagelen=10&path=file&page=opaque"
		if out, failed := call(t, cs, "list_commits", map[string]any{"repo": "repo-a", "path": "file", "cursor": cursor}); !failed || !strings.Contains(out, "server policy") {
			t.Fatalf("policy did not deny: %s", out)
		}
	}
	for _, policy := range []Policy{{Projects: []string{"APP"}}, {Projects: []string{"OTHER"}, Repositories: []string{"repo-a"}}} {
		project := "APP"
		reads := 0
		cs := connectUUID(t, policy, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Has("fields") {
				writeJSON(w, map[string]any{"project": map[string]string{"key": project}})
				return
			}
			reads++
			if r.URL.Query().Has("page") {
				_, _ = fmt.Fprint(w, `{"values":[]}`)
				return
			}
			next := *r.URL
			q := next.Query()
			q.Set("page", "opaque")
			next.RawQuery = q.Encode()
			writeJSON(w, map[string]any{"values": []bitbucket.HistoryCommit{{Hash: "first"}}, "next": next.String()})
		})
		first := decodeResult[listCommitsResult](t, mustCall(t, cs, "list_commits", map[string]any{"repo": "repo-a"}))
		project = "MOVED"
		out, failed := call(t, cs, "list_commits", map[string]any{"repo": "repo-a", "cursor": first.NextCursor})
		if len(policy.Repositories) == 0 {
			if !failed || reads != 1 {
				t.Fatalf("moved repository read: %s, reads %d", out, reads)
			}
		} else if failed || reads != 2 {
			t.Fatalf("explicit repository exception rejected: %s", out)
		}
	}
}

func TestHistoryBoundsResponses(t *testing.T) {
	cs := connectUUID(t, Policy{}, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"values": []bitbucket.HistoryCommit{{Hash: "first", Message: strings.Repeat("x", sourceChunkBytes)}}})
	})
	if out, failed := call(t, cs, "list_commits", map[string]any{"repo": "repo-a"}); !failed || !strings.Contains(out, "reduce limit") {
		t.Fatal(out)
	}
}
