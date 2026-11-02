package bitbucket

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestListRefsBoundsPagesAndReturnsErrors(t *testing.T) {
	for _, kind := range []string{"branches", "tags"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/2.0/repositories/ws/repo-a/refs/"+kind || r.URL.Query().Get("sort") != "name" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("pagelen") != "1" {
					t.Errorf("request = %s", r.URL)
				}
				_, _ = fmt.Fprintf(w, `{"values":[{"name":"release/next","target":{"hash":%q},"message":"tag message"}],"size":3,"next":"https://evil.invalid/steal"}`, testHead)
			})
			p, err := c.ListRefs(t.Context(), "repo-a", kind, 2, 1)
			if err != nil || len(p.Values) != 1 || p.Values[0].Target.Hash != testHead || !p.More || *p.Total != 3 || requests != 1 {
				t.Fatalf("page = %+v, error = %v, requests = %d", p, err, requests)
			}
		})
	}
	c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
	})
	if _, err := c.ListRefs(t.Context(), "repo-a", "tags", 1, 10); !IsStatus(err, 429) {
		t.Fatal(err)
	}
	c = newTestClient(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"values":[{},{}]}`) })
	if _, err := c.ListRefs(t.Context(), "repo-a", "branches", 1, 1); err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatal(err)
	}
}

func TestStopPipelineRequestAndErrors(t *testing.T) {
	for _, status := range []int{204, 400, 403, 404, 429} {
		c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.EscapedPath() != "/2.0/repositories/ws/repo-a/pipelines/%7Bpipeline%7D/stopPipeline" {
				t.Errorf("request = %s %s", r.Method, r.URL)
			}
			w.WriteHeader(status)
		})
		err := c.StopPipeline(t.Context(), "repo-a", "{pipeline}")
		if status == 204 && err != nil || status != 204 && !IsStatus(err, status) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}
