package bitbucket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestHistoryPreservesOpaqueContinuation(t *testing.T) {
	for _, mode := range []string{"all_refs", "revision", "filehistory"} {
		t.Run(mode, func(t *testing.T) {
			revision, path, renames := "", "src/pay*", mode == "filehistory"
			endpoint := APIURL + "/repositories/ws/repo-a/commits"
			query := url.Values{"pagelen": {"1"}, "path": {path}}
			if mode != "all_refs" {
				revision = "release/next"
				endpoint += escapedPath(revision)
			}
			if renames {
				path = "src/a b.go"
				endpoint = APIURL + "/repositories/ws/repo-a/filehistory" + escapedPath(revision, "src", "a b.go")
				query = url.Values{"pagelen": {"1"}, "renames": {"true"}}
			}
			// Keep the order, encoding and unknown iterator parameter byte for byte.
			cursor := endpoint + "?ctx=%2fopaque%2B%3D&page=bookmark%2Fabc%2B%3D&" + query.Encode()
			requests := 0
			c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Query().Get("pagelen") != "1" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				if requests == 1 && r.URL.Query().Has("page") || requests == 2 && r.URL.String() != cursor {
					t.Fatalf("history cursor was reconstructed: %s", r.URL)
				}
				commit := HistoryCommit{Hash: "first", Message: "untrusted", Parents: []Commit{{Hash: "parent"}}}
				commit.Author.User.DisplayName = "Developer"
				payload := map[string]any{"size": 2}
				if requests == 1 {
					payload["next"] = cursor
				} else {
					commit.Hash = "last"
				}
				if renames {
					payload["values"] = []HistoryEntry{{Path: "old/name.go", Commit: commit}}
				} else {
					payload["values"] = []HistoryCommit{commit}
				}
				if err := json.NewEncoder(w).Encode(payload); err != nil {
					t.Fatal(err)
				}
			})
			first, err := c.ListCommits(t.Context(), "repo-a", revision, path, renames, "", 1)
			if err != nil || requests != 1 || first.NextCursor != cursor || first.Total == nil || *first.Total != 2 || len(first.Values) != 1 || first.Values[0].Commit.Author.User.DisplayName != "Developer" || len(first.Values[0].Commit.Parents) != 1 {
				t.Fatalf("first page: %+v %v, requests %d", first, err, requests)
			}
			last, err := c.ListCommits(t.Context(), "repo-a", revision, path, renames, first.NextCursor, 1)
			if err != nil || requests != 2 || last.NextCursor != "" || len(last.Values) != 1 || last.Values[0].Commit.Hash != "last" {
				t.Fatalf("last page: %+v %v, requests %d", last, err, requests)
			}
		})
	}
}

func TestHistoryFollowsFileRenames(t *testing.T) {
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/2.0/repositories/ws/repo-a/filehistory/main/src/a%20b.go" || r.URL.Query().Get("renames") != "true" || r.URL.Query().Has("path") {
			t.Fatal(r.URL)
		}
		_, _ = fmt.Fprint(w, `{"values":[{"path":"old/name.go","commit":{"hash":"abc","date":"2026-11-03","message":"rename"}}]}`)
	})
	out, err := c.ListCommits(t.Context(), "repo-a", "main", "src/a b.go", true, "", 10)
	if err != nil || out.NextCursor != "" || len(out.Values) != 1 || out.Values[0].Path != "old/name.go" || out.Values[0].Commit.Message != "rename" {
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
			if _, err := c.ListCommits(t.Context(), "repo-a", "main", "file", renames, "", 10); !IsStatus(err, status) || !strings.Contains(err.Error(), "history unavailable") {
				t.Fatalf("status %d: %v", status, err)
			}
		}
	}
	c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"values":[{"hash":"one"},{"hash":"two"}]}`)
	})
	if _, err := c.ListCommits(t.Context(), "repo-a", "", "file", false, "", 1); err == nil {
		t.Fatal("accepted more records than requested")
	}
}

func TestHistoryRejectsChangedCursorsBeforeUse(t *testing.T) {
	valid := APIURL + "/repositories/ws/repo-a/commits/main?pagelen=1&path=file&page=opaque"
	for _, cursor := range []string{
		strings.Replace(valid, "https://api.bitbucket.org", "https://evil.example", 1),
		strings.Replace(valid, "https://api.bitbucket.org", "http://api.bitbucket.org", 1),
		strings.Replace(valid, "https://api.bitbucket.org", "https://user:pass@api.bitbucket.org", 1),
		strings.Replace(valid, "https://api.bitbucket.org", "https://api.bitbucket.org:443", 1),
		strings.Replace(valid, "/ws/", "/other/", 1), strings.Replace(valid, "/repo-a/", "/repo-b/", 1),
		strings.Replace(valid, "/commits/", "/filehistory/", 1), strings.Replace(valid, "/main?", "/other?", 1),
		strings.Replace(valid, "path=file", "path=other", 1), strings.Replace(valid, "path=file", "", 1),
		strings.Replace(valid, "pagelen=1", "pagelen=2", 1), valid + "&pagelen=1", valid + "&path=other",
		valid + "&include=other", valid + "&exclude=main", valid + "&renames=true", valid + "&sort=date",
		valid + "&q=other", valid + "&fields=values.hash", valid + "#fragment", valid + "&ctx=%ZZ",
		"/2.0/repositories/ws/repo-a/commits/main?pagelen=1&path=file&page=opaque",
		valid + "&ctx=" + strings.Repeat("x", maxHistoryCursorBytes),
	} {
		for _, fromResponse := range []bool{false, true} {
			requests := 0
			c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				if !fromResponse {
					t.Error("invalid cursor reached transport")
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"values": []HistoryCommit{{Hash: "first"}}, "next": cursor}); err != nil {
					t.Fatal(err)
				}
			})
			input := cursor
			if fromResponse {
				input = ""
			}
			out, err := c.ListCommits(t.Context(), "repo-a", "main", "file", false, input, 1)
			if err == nil || out.NextCursor != "" || requests > 1 || !fromResponse && requests != 0 {
				t.Fatalf("invalid cursor accepted: %+v %v, requests %d", out, err, requests)
			}
		}
	}
	fileCursor := APIURL + "/repositories/ws/repo-a/filehistory/main/file?pagelen=1&renames=false&page=opaque"
	c := newTestClient(func(http.ResponseWriter, *http.Request) { t.Error("changed rename mode reached transport") })
	if _, err := c.ListCommits(t.Context(), "repo-a", "main", "file", true, fileCursor, 1); err == nil {
		t.Fatal("changed file-history rename mode accepted")
	}
}
