package bitbucket

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSourceFilePinsEscapesAndReadsRawText(t *testing.T) {
	var requests []string
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		if user, token, ok := r.BasicAuth(); !ok || user != testEmail || token != testToken {
			t.Error("missing Basic auth")
		}
		switch {
		case strings.Contains(r.URL.Path, "/commit/"):
			_, _ = fmt.Fprintf(w, `{"hash":%q}`, testHead)
		case r.URL.Query().Get("format") == "meta":
			_, _ = fmt.Fprint(w, `{"type":"commit_file","size":6}`)
		default:
			_, _ = fmt.Fprint(w, "hello\n")
		}
	})
	sha, err := c.ResolveRevision(t.Context(), "repo-a", "release/next")
	if err != nil || sha != testHead {
		t.Fatalf("revision = %s, %v", sha, err)
	}
	data, err := c.SourceFile(t.Context(), "repo-a", sha, "dir/a #?.txt", 100)
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("file = %q, %v", data, err)
	}
	want := []string{
		"/2.0/repositories/ws/repo-a/commit/release%2Fnext?",
		"/2.0/repositories/ws/repo-a/src/" + testHead + "/dir/a%20%23%3F.txt?format=meta",
		"/2.0/repositories/ws/repo-a/src/" + testHead + "/dir/a%20%23%3F.txt?",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %q, want %q", requests, want)
	}
}

func TestSourceFileRejectsWrongTypeOversizeRedirectAndAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, body, want string
		status                     int
	}{
		{"directory", `{"type":"commit_directory"}`, "", "not a file", 200},
		{"size metadata", `{"type":"commit_file","size":101}`, "", "download limit", 200},
		{"size body", `{"type":"commit_file","size":1}`, strings.Repeat("x", 101), "download limit", 200},
		{"LFS", `{"type":"commit_file"}`, "", "external file downloads", 301},
		{"forbidden", `{"type":"commit_file"}`, `{"error":{"message":"forbidden"}}`, "forbidden", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Host != "api.bitbucket.org" {
					t.Fatal("followed an external redirect")
				}
				if r.URL.Query().Get("format") == "meta" {
					_, _ = fmt.Fprint(w, tc.metadata)
					return
				}
				w.Header().Set("Location", "https://storage.invalid/file?secret=signed")
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			})
			_, err := c.SourceFile(t.Context(), "repo-a", testHead, "file", 100)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "signed") {
				t.Fatalf("error = %v", err)
			}
			if requests > 2 {
				t.Fatalf("requests = %d", requests)
			}
		})
	}
}

func TestDirectoryRootPagesAndTypes(t *testing.T) {
	requests := 0
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.HasSuffix(r.URL.Path, "/src/"+testHead+"/") {
			t.Errorf("root path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("format") == "meta" {
			_, _ = fmt.Fprint(w, `{"type":"commit_directory"}`)
			return
		}
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("pagelen") != "1" {
			t.Error("missing page bounds")
		}
		_, _ = fmt.Fprint(w, `{"values":[{"type":"commit_file","path":"a","size":7}],"size":4,"next":"https://evil.invalid/steal"}`)
	})
	p, err := c.ListDirectory(t.Context(), "repo-a", testHead, "", 2, 1)
	if err != nil || len(p.Values) != 1 || p.Values[0].Size != 7 || !p.More || *p.Total != 4 || requests != 2 {
		t.Fatalf("page = %+v, requests = %d, error = %v", p, requests, err)
	}
	c = newTestClient(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"type":"commit_file"}`) })
	if _, err = c.ListDirectory(t.Context(), "repo-a", testHead, "file", 1, 10); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatal(err)
	}
}

func TestResolveRevisionRejectsInvalidUpstreamCommit(t *testing.T) {
	c := newTestClient(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"hash":"short"}`) })
	if _, err := c.ResolveRevision(t.Context(), "repo-a", "main"); err == nil {
		t.Fatal("accepted an abbreviated commit")
	}
}
