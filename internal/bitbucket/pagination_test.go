package bitbucket

import (
	"fmt"
	"net/http"
	"testing"
)

func TestAdditionalCollectionsFetchOnlyRequestedPage(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		read       func(*Client) (bool, *int, error)
	}{
		{"repository variables", "/2.0/repositories/ws/repo-a/pipelines_config/variables", func(c *Client) (bool, *int, error) {
			p, err := c.ListVariablesPage(t.Context(), VariableScope{Repo: "repo-a"}, 3, 1)
			return p.More, p.Total, err
		}},
		{"workspace variables", "/2.0/workspaces/ws/pipelines-config/variables", func(c *Client) (bool, *int, error) {
			p, err := c.ListVariablesPage(t.Context(), VariableScope{}, 3, 1)
			return p.More, p.Total, err
		}},
		{"deployment variables", "/2.0/repositories/ws/repo-a/deployments_config/environments/{env}/variables", func(c *Client) (bool, *int, error) {
			p, err := c.ListVariablesPage(t.Context(), VariableScope{Repo: "repo-a", EnvironmentUUID: "{env}"}, 3, 1)
			return p.More, p.Total, err
		}},
		{"schedules", "/2.0/repositories/ws/repo-a/pipelines_config/schedules", func(c *Client) (bool, *int, error) {
			p, err := c.ListSchedulesPage(t.Context(), "repo-a", 3, 1)
			return p.More, p.Total, err
		}},
		{"steps", "/2.0/repositories/ws/repo-a/pipelines/{run}/steps/", func(c *Client) (bool, *int, error) {
			p, err := c.ListStepsPage(t.Context(), "repo-a", "{run}", 3, 1)
			return p.More, p.Total, err
		}},
		{"PR builds", "/2.0/repositories/ws/repo-a/pullrequests/7/statuses", func(c *Client) (bool, *int, error) {
			p, err := c.PullRequestStatuses(t.Context(), "repo-a", 7, 3, 1)
			return p.More, p.Total, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, response := range []struct {
				body      string
				status    int
				wantError bool
			}{
				{`{"values":[{}],"size":8,"next":"https://evil.invalid/steal"}`, 200, false},
				{`{"values":[]}`, 200, false},
				{`{"values":[{},{}]}`, 200, true},
				{`{"error":{"message":"denied"}}`, 403, true},
				{`{"error":{"message":"slow down"}}`, 429, true},
			} {
				requests := 0
				c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
					requests++
					if r.URL.Host != "api.bitbucket.org" || r.URL.Path != tc.path || r.URL.Query().Get("page") != "3" || r.URL.Query().Get("pagelen") != "1" {
						t.Errorf("request = %s", r.URL)
					}
					if _, token, ok := r.BasicAuth(); !ok || token != testToken {
						t.Error("missing Basic auth")
					}
					w.WriteHeader(response.status)
					_, _ = fmt.Fprint(w, response.body)
				})
				more, total, err := tc.read(c)
				if (err != nil) != response.wantError || requests != 1 {
					t.Fatalf("error = %v, requests = %d", err, requests)
				}
				if !response.wantError && ((total != nil) != more || more && *total != 8) {
					t.Fatalf("more = %v, total = %v", more, total)
				}
			}
		})
	}
}
