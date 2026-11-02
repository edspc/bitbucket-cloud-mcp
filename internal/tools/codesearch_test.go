package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/edspc/bitbucket-custom-mcp/internal/bitbucket"
)

func TestSearchCodeRequestAndPagination(t *testing.T) {
	calls := 0
	cs := connectUUID(t, Policy{Repositories: []string{"repo-a"}}, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/2.0/workspaces/ws/codesearch" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		email, token, ok := r.BasicAuth()
		if !ok || email != "dev@example.com" || token != "api-secret" {
			t.Error("missing configured auth")
		}
		var body bitbucket.CodeSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		filters := body.Query.Filters
		if body.Query.SearchTerm != "handlePayment" || body.Config.MaxResults != 20 || filters == nil || len(filters.And) != 4 {
			t.Fatalf("unexpected body: %+v", body)
		}
		if filters.And[0].Attribute != "repository" || filters.And[0].Eq != "repo-a" || filters.And[3].Wildcard != "src/*" {
			t.Fatalf("filters: %+v", filters)
		}
		if calls == 2 && (body.RequestID != "request-1" || body.Pagination == nil || body.Pagination.NextCursor != "opaque") {
			t.Fatal("pagination lost")
		}
		_, _ = fmt.Fprint(w, `{"id":"request-1","pagination":{"has_more":true,"next_cursor":"opaque"},"metadata":{"total_count":999},"repositories":[{"name":"hidden"}],"matched_files":[{"path":"src/main.go","url":"https://bitbucket.org/ws/repo-a/src/abc/src/main.go","version":"abc","language":null,"matched_chunks":[{"source_code":"handlePayment()","line_range":{"start":1,"end":1},"matched_line_numbers":[1],"matched_ranges":[[1,0,13]]}]}]}`)
	})
	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == "search_code" {
			found = true
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatal("search_code must be annotated read-only")
			}
		}
	}
	if !found {
		t.Fatal("search_code was not registered")
	}
	args := map[string]any{"query": "handlePayment", "repo": "repo-a", "language": "go", "extension": "go", "path": "src/*"}
	for range 2 {
		result := mustCall(t, cs, "search_code", args)
		out := decodeResult[searchCodeResult](t, result)
		if out.ContentTrust != "untrusted" || out.Metadata != nil || len(out.MatchedFiles) != 1 || len(out.MatchedFiles[0].MatchedChunks) != 1 {
			t.Fatalf("result: %+v", out)
		}
		args["request_id"] = out.ID
		args["next_cursor"] = out.Pagination.NextCursor
	}
}

func TestSearchCodeDeniesBeforeSearch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy Policy
		args   map[string]any
	}{
		{"tool", Policy{Tools: []string{"get_uuid"}}, map[string]any{"query": "x"}},
		{"repo", Policy{Repositories: []string{"repo-a"}}, map[string]any{"query": "x", "repo": "repo-b"}},
		{"empty repos", Policy{Repositories: []string{}}, map[string]any{"query": "x", "repo": "repo-a"}},
		{"empty query", Policy{}, map[string]any{"query": "  "}},
		{"long query", Policy{}, map[string]any{"query": strings.Repeat("я", 1025)}},
		{"negative limit", Policy{}, map[string]any{"query": "x", "limit": -1}},
		{"large limit", Policy{}, map[string]any{"query": "x", "limit": 101}},
		{"fractional limit", Policy{}, map[string]any{"query": "x", "limit": 1.5}},
		{"cursor only", Policy{}, map[string]any{"query": "x", "next_cursor": "opaque"}},
		{"id only", Policy{}, map[string]any{"query": "x", "request_id": "id"}},
		{"path escape", Policy{}, map[string]any{"query": "x", "repo": "../other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := connectUUID(t, tc.policy, func(http.ResponseWriter, *http.Request) {
				t.Error("denied search reached API")
			})
			if out, isErr := call(t, cs, "search_code", tc.args); !isErr {
				t.Fatalf("accepted: %s", out)
			}
		})
	}
}

func TestSearchCodeRechecksProjectOnNextPage(t *testing.T) {
	project := "APP"
	searches := 0
	cs := connectUUID(t, Policy{Projects: []string{"APP"}}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != "/2.0/repositories/ws/repo-a" {
				t.Errorf("metadata URL: %s", r.URL)
			}
			writeJSON(w, map[string]any{"project": map[string]string{"key": project}})
			return
		}
		searches++
		_, _ = fmt.Fprint(w, `{"id":"id","pagination":{"has_more":true,"next_cursor":"cursor"},"matched_files":[]}`)
	})
	mustCall(t, cs, "search_code", map[string]any{"query": "x", "repo": "repo-a"})
	project = "DENIED"
	if out, isErr := call(t, cs, "search_code", map[string]any{"query": "x", "repo": "repo-a", "request_id": "id", "next_cursor": "cursor"}); !isErr {
		t.Fatalf("moved repository accepted: %s", out)
	}
	if searches != 1 {
		t.Fatalf("searches = %d", searches)
	}
}

func TestSearchCodeRejectsWiderResponse(t *testing.T) {
	for _, fileURL := range []string{"https://bitbucket.org/ws/repo-b/src/abc/main.go", "https://bitbucket.org/other/repo-a/src/abc/main.go", "https://example.com/ws/repo-a/src/abc/main.go", "invalid"} {
		t.Run(fileURL, func(t *testing.T) {
			cs := connectUUID(t, Policy{Repositories: []string{"repo-a"}}, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, map[string]any{"matched_files": []map[string]string{{"url": fileURL, "path": "secret.go"}}})
			})
			out, isErr := call(t, cs, "search_code", map[string]any{"query": "x", "repo": "repo-a"})
			if !isErr || strings.Contains(out, "secret.go") {
				t.Fatalf("wider result leaked: %s", out)
			}
		})
	}
}

func TestSearchCodeWorkspaceAndErrors(t *testing.T) {
	for _, status := range []int{200, 400, 401, 403, 404, 415, 429, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			cs := connectUUID(t, Policy{}, func(w http.ResponseWriter, r *http.Request) {
				var body bitbucket.CodeSearchRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Query.Filters != nil {
					t.Fatal("unexpected workspace filter")
				}
				w.WriteHeader(status)
				if status == 200 {
					_, _ = fmt.Fprint(w, `{"id":"id","matched_files":[],"metadata":{"total_count":0}}`)
				} else {
					_, _ = fmt.Fprint(w, `{"error":{"message":"search unavailable"}}`)
				}
			})
			out, isErr := call(t, cs, "search_code", map[string]any{"query": "x"})
			if isErr != (status != 200) {
				t.Fatalf("status %d: %s", status, out)
			}
			if status != 200 && !strings.Contains(out, "search unavailable") {
				t.Fatalf("error lost: %s", out)
			}
		})
	}
}
