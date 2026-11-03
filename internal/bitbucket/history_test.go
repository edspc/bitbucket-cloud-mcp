package bitbucket

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestHistoryReadsOnlyRequestedPage(t *testing.T) {
	for _, revision := range []string{"", "release/next"} {
		requests := 0
		c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
			requests++
			if r.Method != http.MethodGet || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("pagelen") != "1" || r.URL.Query().Get("path") != "src/pay*" {
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
			}
			if revision != "" && !strings.HasSuffix(r.URL.EscapedPath(), "/release%2Fnext") {
				t.Fatal(r.URL)
			}
			_, _ = fmt.Fprint(w, `{"values":[{"hash":"abc","message":"untrusted","author":{"raw":"dev","user":{"display_name":"Developer"}},"parents":[{"hash":"parent"}]}],"size":3,"next":"https://evil.example/collect"}`)
		})
		out, err := c.ListCommits(t.Context(), "repo-a", revision, "src/pay*", false, 2, 1)
		if err != nil || requests != 1 || !out.More || out.Total == nil || *out.Total != 3 || len(out.Values) != 1 || out.Values[0].Commit.Author.User.DisplayName != "Developer" || len(out.Values[0].Commit.Parents) != 1 {
			t.Fatalf("history: %+v %v, requests %d", out, err, requests)
		}
	}
}

func TestHistoryFollowsFileRenames(t *testing.T) {
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/2.0/repositories/ws/repo-a/filehistory/main/src/a%20b.go" || r.URL.Query().Get("renames") != "true" || r.URL.Query().Has("path") {
			t.Fatal(r.URL)
		}
		_, _ = fmt.Fprint(w, `{"values":[{"path":"old/name.go","commit":{"hash":"abc","date":"2026-11-03","message":"rename"}}]}`)
	})
	out, err := c.ListCommits(t.Context(), "repo-a", "main", "src/a b.go", true, 1, 10)
	if err != nil || out.More || len(out.Values) != 1 || out.Values[0].Path != "old/name.go" || out.Values[0].Commit.Message != "rename" {
		t.Fatalf("history: %+v %v", out, err)
	}
}

func TestHistoryErrorsAndOversizedPages(t *testing.T) {
	for _, renames := range []bool{false, true} {
		for _, status := range []int{403, 404, 429} {
			c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"error":{"message":"history unavailable"}}`)
			})
			if _, err := c.ListCommits(t.Context(), "repo-a", "main", "file", renames, 1, 10); !IsStatus(err, status) || !strings.Contains(err.Error(), "history unavailable") {
				t.Fatalf("status %d: %v", status, err)
			}
		}
	}
	c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"values":[{"hash":"one"},{"hash":"two"}]}`)
	})
	if _, err := c.ListCommits(t.Context(), "repo-a", "", "file", false, 1, 1); err == nil {
		t.Fatal("accepted more records than requested")
	}
}
