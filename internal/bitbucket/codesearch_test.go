package bitbucket

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestCodeSearchRejectsUnsafeFilters(t *testing.T) {
	for _, filter := range []*CodeFilter{
		nil,
		{Attribute: "namespace", Eq: "other"},
		{Or: []CodeFilter{{Attribute: "repository", Eq: "repo-a"}, {Attribute: "repository", Eq: "other"}}},
		{Attribute: "repository", Eq: "other"},
		{Attribute: "repository", Wildcard: "*"},
		{And: []CodeFilter{{Attribute: "repository", Eq: "repo-a"}, {Attribute: "namespace", Eq: "other"}}},
		{And: []CodeFilter{{And: []CodeFilter{{Attribute: "repository", Eq: "repo-a"}}}}},
	} {
		c := newTestClient(func(http.ResponseWriter, *http.Request) {
			t.Error("unsafe filters reached API")
		})
		in := CodeSearchRequest{Query: CodeQuery{SearchTerm: "x", Filters: filter}}
		in.Config.MaxResults = 1
		if _, err := c.SearchCode(t.Context(), "repo-a", in); err == nil {
			t.Fatalf("accepted filters: %+v", filter)
		}
	}
}

func TestCodeSearchRepositoryExceptionAndEmptyPolicy(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			calls := 0
			c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost {
					t.Error("explicit repository exception should not need project metadata")
				}
				_, _ = fmt.Fprint(w, `{"matched_files":[]}`)
			})
			repos := []string{}
			if allowed {
				repos = append(repos, "repo-a")
			}
			c = c.WithAllowedProjects([]string{}, repos)
			in := CodeSearchRequest{Query: CodeQuery{SearchTerm: "x", Filters: &CodeFilter{Attribute: "repository", Eq: "repo-a"}}}
			in.Config.MaxResults = 1
			_, err := c.SearchCode(context.Background(), "repo-a", in)
			if (err == nil) != allowed || calls != len(repos) {
				t.Fatalf("allowed=%v, calls=%d, err=%v", allowed, calls, err)
			}
		})
	}
}
