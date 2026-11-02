package tools

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

func browseHandler(t *testing.T, requests *[]string, content string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		*requests = append(*requests, r.URL.Path)
		switch {
		case r.URL.Query().Get("fields") == "project.key,project.uuid":
			writeJSON(w, map[string]any{"project": map[string]string{"key": "APP"}})
		case strings.Contains(r.URL.Path, "/commit/"):
			writeJSON(w, bitbucket.Commit{Hash: testPipelineCommit})
		case strings.Contains(r.URL.Path, "/refs/"):
			writeCollectionPage(w, r, []bitbucket.Ref{{Name: "main", Target: bitbucket.Commit{Hash: testPipelineCommit}}, {Name: "release/next", Target: bitbucket.Commit{Hash: testPipelineCommit}}})
		case r.URL.Query().Get("format") == "meta":
			kind := "commit_file"
			if strings.HasSuffix(r.URL.Path, "/") {
				kind = "commit_directory"
			}
			writeJSON(w, bitbucket.SourceEntry{Type: kind, Size: int64(len(content))})
		case strings.HasSuffix(r.URL.Path, "/"):
			writeCollectionPage(w, r, []bitbucket.SourceEntry{{Type: "commit_file", Path: "README.md", Size: 10}, {Type: "commit_directory", Path: "src"}})
		default:
			_, _ = fmt.Fprint(w, content)
		}
	}
}

func TestGetFileChunksPinCommitAndPreserveUTF8(t *testing.T) {
	var requests []string
	text := strings.Repeat("x", sourceChunkBytes-1) + "Ж🙂\nend"
	cs := connectUUID(t, Policy{}, browseHandler(t, &requests, text))
	args := map[string]any{"repo": "repo-a", "revision": "release/next", "path": "src/file.txt"}
	first := decodeResult[getFileResult](t, mustCall(t, cs, "get_file", args))
	if first.ContentTrust != "untrusted" || first.Commit != testPipelineCommit || first.NextOffset != sourceChunkBytes-1 || !utf8.ValidString(first.Content) {
		t.Fatalf("first chunk metadata = commit %s, next %d", first.Commit, first.NextOffset)
	}
	args["revision"] = first.Commit
	args["offset"] = first.NextOffset
	last := decodeResult[getFileResult](t, mustCall(t, cs, "get_file", args))
	if first.Content+last.Content != text || last.NextOffset != 0 {
		t.Fatal("chunks did not reconstruct the file")
	}
	for _, path := range requests[1:] {
		if !strings.Contains(path, "/src/"+testPipelineCommit+"/") {
			t.Fatalf("unpinned request: %s", path)
		}
	}
}

func TestGetFileRejectsBinaryAndBadOffset(t *testing.T) {
	for _, text := range []string{"nul\x00text", "bad\xfftext", "Ж"} {
		var requests []string
		cs := connectUUID(t, Policy{}, browseHandler(t, &requests, text))
		args := map[string]any{"repo": "repo-a", "revision": testPipelineCommit, "path": "file"}
		if text == "Ж" {
			args["offset"] = 1
		}
		if _, failed := call(t, cs, "get_file", args); !failed {
			t.Fatal("accepted binary text or split character")
		}
	}
	var requests []string
	cs := connectUUID(t, Policy{}, browseHandler(t, &requests, ""))
	got := decodeResult[getFileResult](t, mustCall(t, cs, "get_file", map[string]any{"repo": "repo-a", "revision": testPipelineCommit, "path": "empty"}))
	if got.Content != "" || got.Size != 0 || got.NextOffset != 0 {
		t.Fatalf("empty file = %+v", got)
	}
}

func TestDirectoryAndRefsPagination(t *testing.T) {
	for _, tool := range []string{"list_directory", "list_branches", "list_tags"} {
		t.Run(tool, func(t *testing.T) {
			var requests []string
			cs := connectUUID(t, Policy{}, browseHandler(t, &requests, ""))
			args := map[string]any{"repo": "repo-a", "limit": 1}
			if tool == "list_directory" {
				args["revision"] = "main"
				args["path"] = ""
			}
			out := mustCall(t, cs, tool, args)
			if !strings.Contains(out, `"next_page":2`) || !strings.Contains(out, `"content_trust":"untrusted"`) {
				t.Fatal(out)
			}
			args["page"] = 2
			if tool == "list_directory" {
				args["revision"] = testPipelineCommit
			}
			out = mustCall(t, cs, tool, args)
			if strings.Contains(out, `"next_page"`) {
				t.Fatal(out)
			}
			if tool == "list_directory" {
				got := decodeResult[listDirectoryResult](t, out)
				if len(got.Entries) != 1 || got.Entries[0].Path != "src" || got.Commit != testPipelineCommit {
					t.Fatal(out)
				}
			} else {
				got := decodeResult[listRefsResult](t, out)
				if len(got.Refs) != 1 || got.Refs[0].Name != "release/next" || *got.Total != 2 {
					t.Fatal(out)
				}
			}
		})
	}
}

func TestBrowseValidationAndPoliciesBeforeContent(t *testing.T) {
	for _, tool := range []string{"get_file", "list_directory", "list_branches", "list_tags"} {
		for _, policy := range []Policy{{Repositories: []string{}}, {Tools: []string{"get_uuid"}}, {Projects: []string{}}, {Projects: []string{"OTHER"}}} {
			var requests []string
			cs := connectUUID(t, policy, browseHandler(t, &requests, "text"))
			args := map[string]any{"repo": "repo-a"}
			if tool == "get_file" || tool == "list_directory" {
				args["revision"], args["path"] = "main", "file"
			}
			out, failed := call(t, cs, tool, args)
			if !failed || !strings.Contains(out, "server policy") {
				t.Fatalf("policy did not deny %s: %s", tool, out)
			}
			for _, path := range requests {
				if path != "/2.0/repositories/ws/repo-a" {
					t.Fatalf("forbidden content read: %s", path)
				}
			}
		}
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"get_file", map[string]any{"repo": "repo-a", "revision": "main", "path": "../file"}},
		{"get_file", map[string]any{"repo": "repo-a", "revision": "feature/../../other", "path": "file"}},
		{"get_file", map[string]any{"repo": "repo-a", "revision": "main", "path": "/file"}},
		{"get_file", map[string]any{"repo": "repo-a", "revision": "main", "path": "file", "offset": 1}},
		{"get_file", map[string]any{"repo": "repo-a", "revision": "main", "path": "file", "offset": -1}},
		{"get_file", map[string]any{"repo": "repo-a", "revision": "main", "path": ""}},
		{"list_directory", map[string]any{"repo": "repo-a", "revision": "main", "path": "", "page": 2}},
		{"list_directory", map[string]any{"repo": "repo-a", "revision": "main", "path": "", "limit": 51}},
		{"list_tags", map[string]any{"repo": "repo-a", "page": -1}},
		{"list_branches", map[string]any{"repo": "repo-a", "limit": 51}},
		{"list_branches", map[string]any{}},
	} {
		var requests []string
		cs := connectUUID(t, Policy{}, browseHandler(t, &requests, "text"))
		if _, failed := call(t, cs, tc.tool, tc.args); !failed || len(requests) != 0 {
			t.Fatalf("invalid %s requested %v", tc.tool, requests)
		}
	}
}

func TestBrowseProjectUnionAndLiveChecks(t *testing.T) {
	for _, policy := range []Policy{{Projects: []string{"APP"}}, {Projects: []string{"OTHER"}, Repositories: []string{"repo-a"}}} {
		var requests []string
		cs := connectUUID(t, policy, browseHandler(t, &requests, "text"))
		mustCall(t, cs, "get_file", map[string]any{"repo": "repo-a", "revision": testPipelineCommit, "path": "file"})
		want := 4
		if len(policy.Repositories) > 0 {
			want = 2
		}
		if len(requests) != want {
			t.Fatalf("requests = %v", requests)
		}
	}
	var requests []string
	h := browseHandler(t, &requests, "text")
	checks := 0
	cs := connectUUID(t, Policy{Projects: []string{"APP"}}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") != "" {
			checks++
			if checks > 1 {
				writeJSON(w, map[string]any{"project": map[string]string{"key": "OTHER"}})
				return
			}
		}
		h(w, r)
	})
	if _, failed := call(t, cs, "get_file", map[string]any{"repo": "repo-a", "revision": testPipelineCommit, "path": "file"}); !failed {
		t.Fatal("project move allowed a raw file read")
	}
	if len(requests) != 2 {
		t.Fatalf("content was requested after project move: %v", requests)
	}
}

func TestBrowseErrorsAndToolAnnotations(t *testing.T) {
	cs := connectUUID(t, Policy{}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = fmt.Fprint(w, `{"error":{"message":"permission denied"}}`)
	})
	for _, tool := range []string{"get_file", "list_directory", "list_branches", "list_tags"} {
		args := map[string]any{"repo": "repo-a"}
		if tool == "get_file" || tool == "list_directory" {
			args["revision"], args["path"] = "main", "file"
		}
		out, failed := call(t, cs, tool, args)
		if !failed || !strings.Contains(out, "permission denied") {
			t.Fatal(out)
		}
	}
	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tool := range list.Tools {
		switch tool.Name {
		case "get_file", "list_directory", "list_branches", "list_tags":
			found++
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatalf("%s is not read-only", tool.Name)
			}
		case "stop_pipeline":
			found++
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
				t.Fatal("stop_pipeline is not marked as a write")
			}
		}
	}
	if found != 5 {
		t.Fatalf("registered %d of the five tools", found)
	}
}
