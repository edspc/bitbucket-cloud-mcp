package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

func TestSearchCodeAllAllowedRepositories(t *testing.T) {
	var firstFilters string
	searches := 0
	cs := connectUUID(t, Policy{Repositories: []string{"repo-b", "repo-a", "repo-b"}}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		searches++
		var body bitbucket.CodeSearchRequest
		decode(t, r, &body)
		filters := body.Query.Filters
		if filters == nil || len(filters.And) != 2 || len(filters.And[0].Or) != 2 ||
			filters.And[0].Or[0].Eq != "repo-a" || filters.And[0].Or[1].Eq != "repo-b" ||
			filters.And[1].Attribute != "language" || filters.And[1].Eq != "go" {
			t.Fatalf("request does not restrict code to allowed repositories: %+v", filters)
		}
		encoded, err := json.Marshal(filters)
		if err != nil {
			t.Fatal(err)
		}
		if searches == 1 {
			firstFilters = string(encoded)
		} else if string(encoded) != firstFilters || body.RequestID != "id" || body.Pagination == nil || body.Pagination.NextCursor != "cursor" {
			t.Fatal("scope or pagination changed on the next page")
		}
		writeJSON(w, map[string]any{
			"id": "id", "pagination": map[string]any{"has_more": searches == 1, "next_cursor": "cursor"},
			"metadata": map[string]int{"total_count": 999},
			"matched_files": []map[string]string{
				{"path": "a.go", "url": "https://bitbucket.org/ws/repo-a/src/abc/a.go"},
				{"path": "b.go", "url": "https://bitbucket.org/ws/repo-b/src/abc/b.go"},
			},
		})
	})
	args := map[string]any{"query": "payment", "language": "go"}
	for range 2 {
		out := decodeResult[searchCodeResult](t, mustCall(t, cs, "search_code", args))
		if len(out.MatchedFiles) != 2 || out.Metadata != nil {
			t.Fatalf("result: %+v", out)
		}
		args["request_id"] = out.ID
		args["next_cursor"] = out.Pagination.NextCursor
	}
	if searches != 2 {
		t.Fatalf("searches = %d", searches)
	}
}

func TestSearchCodeAllProjectRepositoriesAndExceptions(t *testing.T) {
	var listings, checks, searches int
	cs := connectUUID(t, Policy{Projects: []string{"APP", "{project-uuid}"}, Repositories: []string{"exception"}}, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/2.0/repositories/ws":
			listings++
			if !strings.Contains(r.URL.Query().Get("q"), `project.key = "APP"`) || !strings.Contains(r.URL.Query().Get("q"), `project.uuid = "{project-uuid}"`) {
				t.Fatalf("unrestricted listing: %s", r.URL)
			}
			if r.URL.Query().Get("page") == "1" {
				_, _ = fmt.Fprint(w, `{"values":[{"slug":"by-key","project":{"key":"APP"}},{"slug":"denied","project":{"key":"PRIVATE"}},{"slug":"foreign","full_name":"other/foreign","project":{"key":"APP"}}],"next":"https://api.bitbucket.org/2.0/repositories/ws?page=2"}`)
			} else {
				_, _ = fmt.Fprint(w, `{"values":[{"slug":"by-uuid","project":{"uuid":"{project-uuid}"}}]}`)
			}
		case r.Method == http.MethodGet:
			checks++
			switch r.URL.Path {
			case "/2.0/repositories/ws/by-key":
				_, _ = fmt.Fprint(w, `{"project":{"key":"APP"}}`)
			case "/2.0/repositories/ws/by-uuid":
				_, _ = fmt.Fprint(w, `{"project":{"uuid":"{project-uuid}"}}`)
			default:
				t.Fatalf("unexpected metadata lookup: %s", r.URL)
			}
		case r.Method == http.MethodPost:
			searches++
			var body bitbucket.CodeSearchRequest
			decode(t, r, &body)
			var slugs []string
			for _, filter := range body.Query.Filters.Or {
				slugs = append(slugs, filter.Eq)
			}
			if !reflect.DeepEqual(slugs, []string{"by-key", "by-uuid", "exception"}) {
				t.Fatalf("scope = %v", slugs)
			}
			writeJSON(w, map[string]any{"matched_files": []map[string]string{
				{"url": "https://bitbucket.org/ws/by-key/src/abc/first.go"},
				{"url": "https://bitbucket.org/ws/by-key/src/abc/second.go"},
				{"url": "https://bitbucket.org/ws/by-uuid/src/abc/main.go"},
				{"url": "https://bitbucket.org/ws/exception/src/abc/main.go"},
			}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
	})
	out := decodeResult[searchCodeResult](t, mustCall(t, cs, "search_code", map[string]any{"query": "payment"}))
	if len(out.MatchedFiles) != 4 || listings != 2 || checks != 2 || searches != 1 {
		t.Fatalf("files=%d listings=%d checks=%d searches=%d", len(out.MatchedFiles), listings, checks, searches)
	}
}

func TestSearchCodeEmptyWorkspacePolicyMakesNoSearch(t *testing.T) {
	for _, policy := range []Policy{
		{Repositories: []string{}},
		{Projects: []string{}},
		{Projects: []string{"APP"}},
	} {
		cs := connectUUID(t, policy, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/2.0/repositories/ws" {
				t.Fatalf("empty scope reached search: %s %s", r.Method, r.URL)
			}
			_, _ = fmt.Fprint(w, `{"values":[]}`)
		})
		out := decodeResult[searchCodeResult](t, mustCall(t, cs, "search_code", map[string]any{"query": "x"}))
		if len(out.MatchedFiles) != 0 || out.Pagination.HasMore || out.Metadata != nil {
			t.Fatalf("empty policy result: %+v", out)
		}
	}
}

func TestSearchCodeWorkspaceFailsClosed(t *testing.T) {
	for _, mode := range []string{"listing error", "foreign result", "moved project", "metadata error"} {
		t.Run(mode, func(t *testing.T) {
			cs := connectUUID(t, Policy{Projects: []string{"APP"}}, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/2.0/repositories/ws":
					if mode == "listing error" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					_, _ = fmt.Fprint(w, `{"values":[{"slug":"repo-a","project":{"key":"APP"}}]}`)
				case r.Method == http.MethodPost:
					slug := "repo-a"
					if mode == "foreign result" {
						slug = "private"
					}
					writeJSON(w, map[string]any{"matched_files": []map[string]any{{"url": "https://bitbucket.org/ws/" + slug + "/src/abc/secret.go", "matched_chunks": []map[string]string{{"source_code": "private-code-marker"}}}}})
				case r.Method == http.MethodGet:
					if mode == "metadata error" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					_, _ = fmt.Fprint(w, `{"project":{"key":"PRIVATE"}}`)
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
			})
			out, isErr := call(t, cs, "search_code", map[string]any{"query": "payment"})
			if !isErr || strings.Contains(out, "private-code-marker") || strings.Contains(out, "secret.go") {
				t.Fatalf("unsafe workspace result: %s", out)
			}
		})
	}
}

func TestSearchCodeRefreshesProjectScopeBetweenCursorPages(t *testing.T) {
	searches := 0
	cs := connectUUID(t, Policy{Projects: []string{"APP"}}, func(w http.ResponseWriter, r *http.Request) {
		slug := "repo-a"
		if searches > 0 {
			slug = "repo-b"
		}
		if r.Method == http.MethodGet && r.URL.Path == "/2.0/repositories/ws" {
			writeJSON(w, map[string]any{"values": []map[string]any{{"slug": slug, "project": map[string]string{"key": "APP"}}}})
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		var body bitbucket.CodeSearchRequest
		decode(t, r, &body)
		if body.Query.Filters == nil || len(body.Query.Filters.Or) != 1 || body.Query.Filters.Or[0].Eq != slug {
			t.Fatalf("stale search scope: %+v", body.Query.Filters)
		}
		searches++
		if searches == 1 {
			_, _ = fmt.Fprint(w, `{"id":"id","pagination":{"has_more":true,"next_cursor":"cursor"},"matched_files":[]}`)
			return
		}
		if body.RequestID != "id" || body.Pagination == nil || body.Pagination.NextCursor != "cursor" {
			t.Fatal("cursor was not passed to Bitbucket for validation")
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"message":"query filters changed; restart search"}}`)
	})
	mustCall(t, cs, "search_code", map[string]any{"query": "payment"})
	out, isErr := call(t, cs, "search_code", map[string]any{"query": "payment", "request_id": "id", "next_cursor": "cursor"})
	if !isErr || !strings.Contains(out, "restart search") || searches != 2 {
		t.Fatalf("changed scope should fail without stale results: %s, searches=%d", out, searches)
	}
}
