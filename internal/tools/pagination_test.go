package tools

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

func TestVariablePagesInEveryScopeRedactSecrets(t *testing.T) {
	for _, scope := range []string{"workspace", "repository", "deployment"} {
		t.Run(scope, func(t *testing.T) {
			f := newFake()
			key := "workspace"
			args := map[string]any{"scope": scope}
			if scope != "workspace" {
				args["repo"] = "repo-b"
				key = "repo-b"
			}
			if scope == "deployment" {
				args["environment"] = "Test"
				key = "repo-b/{env-test}"
			}
			for i := range 23 {
				f.vars[key] = append(f.vars[key], bitbucket.Variable{UUID: fmt.Sprint(i), Key: fmt.Sprintf("VAR_%02d", i), Value: "secret-marker", Secured: true})
			}
			cs := connect(t, f, nil)
			for page := 1; page <= 3; page++ {
				if page > 1 {
					args["page"] = page
				}
				out := mustCall(t, cs, "list_variables", args)
				got := decodeResult[listVariablesResult](t, out)
				wantCount := 10
				if page == 3 {
					wantCount = 3
				}
				if len(got.Variables) != wantCount || got.Variables[0].Name != fmt.Sprintf("VAR_%02d", (page-1)*10) || got.Total == nil || *got.Total != 23 || got.NextPage != nextPage(page, page < 3) || strings.Contains(out, "secret-marker") {
					t.Fatal(out)
				}
			}
		})
	}
}

func TestSchedulePages(t *testing.T) {
	f := newFake()
	for i := range 23 {
		f.schedules["repo-a"] = append(f.schedules["repo-a"], bitbucket.Schedule{UUID: fmt.Sprint(i), Target: bitbucket.RefTarget("branch", "main", "")})
	}
	cs := connect(t, f, nil)
	for _, page := range []int{1, 2, 3, 1000000000} {
		got := decodeResult[listSchedulesResult](t, mustCall(t, cs, "list_schedules", map[string]any{"repo": "repo-a", "page": page}))
		want := 10
		if page == 3 {
			want = 3
		}
		if page > 3 {
			want = 0
		}
		if len(got.Schedules) != want || got.NextPage != nextPage(page, page < 3) || got.Total == nil || *got.Total != 23 {
			t.Fatalf("page %d = %+v", page, got)
		}
		if want > 0 && got.Schedules[0].UUID != fmt.Sprint((page-1)*10) {
			t.Fatal(got)
		}
	}
}

func TestPipelineStepPagesAndLogSelection(t *testing.T) {
	f := newFake()
	var steps []bitbucket.Step
	for i := range 23 {
		steps = append(steps, bitbucket.Step{UUID: fmt.Sprintf("{step-%d}", i), Name: fmt.Sprintf("step-%d", i), StartedOn: "started", State: completed("SUCCESSFUL")})
	}
	p := f.addRun("repo-a", completed("SUCCESSFUL"), steps...)
	f.logs[steps[22].UUID] = "last step log"
	cs := connect(t, f, nil)
	for _, page := range []int{1, 2, 3, 1000000000} {
		args := map[string]any{"repo": "repo-a", "pipeline": p.UUID, "steps_page": page}
		got := decodeResult[pipelineView](t, mustCall(t, cs, "get_pipeline", args))
		log := decodeResult[pipelineLogResult](t, mustCall(t, cs, "get_pipeline_log", args))
		want := 10
		if page == 3 {
			want = 3
		}
		if page > 3 {
			want = 0
		}
		if len(got.Steps) != want || got.StepsTotal == nil || *got.StepsTotal != 23 || got.StepsNextPage != nextPage(page, page < 3) || !slices.Equal(got.Steps, log.Steps) || log.StepsNextPage != got.StepsNextPage || log.StepsTotal != 23 || log.Step.UUID != steps[22].UUID || log.Log != "last step log" {
			t.Fatalf("page %d = %+v; log = %+v", page, got, log)
		}
	}
	watch := decodeResult[watchPipelineResult](t, mustCall(t, cs, "watch_pipeline", map[string]any{"repo": "repo-a", "pipeline": p.UUID}))
	if watch.Pipeline == nil || len(watch.Pipeline.Steps) != 23 {
		t.Fatal("monitoring lost steps outside the default page")
	}
}

func TestPullRequestBuildPagesSkipFiles(t *testing.T) {
	f := newFake()
	addPullRequest(f)
	f.prStatuses["repo-a/7"] = nil
	for i := range 57 {
		f.prStatuses["repo-a/7"] = append(f.prStatuses["repo-a/7"], bitbucket.CommitStatus{Name: fmt.Sprint(i), State: "SUCCESSFUL"})
	}
	cs := connect(t, f, nil)
	first := decodeResult[pullRequestView](t, mustCall(t, cs, "get_pull_request", map[string]any{"repo": "repo-a", "pull_request": 7}))
	if len(first.Builds) != 50 || first.BuildsNextPage != 2 {
		t.Fatalf("first builds = %+v", first.Builds)
	}
	var got pullRequestView
	requests := prRequestsOf(f, func() {
		got = decodeResult[pullRequestView](t, mustCall(t, cs, "get_pull_request", map[string]any{"repo": "repo-a", "pull_request": 7, "builds_page": first.BuildsNextPage}))
	})
	if len(got.Builds) != 7 || got.Builds[0].Name != "50" || got.BuildsNextPage != 0 || got.BuildsTruncated || got.BuildsTotal != 57 || len(got.Files) > 0 || len(got.Reviewers) > 0 || len(requests) != 2 {
		t.Fatalf("continuation = %+v; requests = %v", got, requests)
	}
	for _, page := range []int{1, 2, 3} {
		got = decodeResult[pullRequestView](t, mustCall(t, cs, "get_pull_request", map[string]any{"repo": "repo-a", "pull_request": 7, "builds_page": page, "builds_limit": 2}))
		if len(got.Builds) != 2 || got.Builds[0].Name != fmt.Sprint((page-1)*2) || got.BuildsNextPage != page+1 {
			t.Fatal(got.Builds)
		}
	}
}

func TestPaginationValidatesBeforeAPIRequests(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"list_variables", map[string]any{"scope": "deployment", "repo": "repo-b", "environment": "Test", "page": -1}},
		{"list_variables", map[string]any{"scope": "workspace", "limit": 51}},
		{"list_schedules", map[string]any{"repo": "repo-a", "page": -1}},
		{"list_schedules", map[string]any{"repo": "repo-a", "limit": 51}},
		{"get_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1", "steps_page": -1}},
		{"get_pipeline_log", map[string]any{"repo": "repo-a", "pipeline": "1", "steps_limit": 51}},
		{"get_pull_request", map[string]any{"repo": "repo-a", "pull_request": 7, "builds_page": -1}},
		{"get_pull_request", map[string]any{"repo": "repo-a", "pull_request": 7, "builds_limit": 51}},
		{"get_pull_request", map[string]any{"repo": "repo-a", "pull_request": 7, "builds_page": 2, "paths": []string{"a"}}},
		{"get_pull_request", map[string]any{"repo": "repo-a", "pull_request": 7, "builds_page": 2, "files_from": "cursor"}},
	} {
		requests := 0
		cs := connectUUID(t, Policy{}, func(http.ResponseWriter, *http.Request) { requests++ })
		if _, failed := call(t, cs, tc.tool, tc.args); !failed || requests != 0 {
			t.Fatalf("invalid %s made %d requests", tc.tool, requests)
		}
	}
}

func TestPaginationPreservesPolicyOnLaterPages(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"list_variables", map[string]any{"scope": "repository", "repo": "repo-a", "page": 2}},
		{"list_variables", map[string]any{"scope": "workspace", "page": 2}},
		{"list_schedules", map[string]any{"repo": "repo-a", "page": 2}},
		{"get_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1", "steps_page": 2}},
		{"get_pipeline_log", map[string]any{"repo": "repo-a", "pipeline": "1", "steps_page": 2}},
		{"get_pull_request", map[string]any{"repo": "repo-a", "pull_request": 7, "builds_page": 2}},
	} {
		for _, policy := range []Policy{{Repositories: []string{}}, {Projects: []string{}}, {Projects: []string{"OTHER"}}, {Tools: []string{"get_uuid"}}} {
			contentReads := 0
			cs := connectUUID(t, policy, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("fields") == "project.key,project.uuid" {
					writeJSON(w, map[string]any{"project": map[string]string{"key": "APP"}})
					return
				}
				contentReads++
			})
			if _, failed := call(t, cs, tc.tool, tc.args); !failed || contentReads != 0 {
				t.Fatalf("%s bypassed policy", tc.tool)
			}
		}
	}
}

func TestPaginatedReadsDoNotLimitWritePreflight(t *testing.T) {
	f := newFake()
	for i := range 101 {
		f.vars["repo-a"] = append(f.vars["repo-a"], bitbucket.Variable{UUID: fmt.Sprint(i), Key: fmt.Sprintf("VAR_%d", i), Value: "plain"})
		f.schedules["repo-a"] = append(f.schedules["repo-a"], bitbucket.Schedule{UUID: fmt.Sprint(i), Enabled: true, Target: bitbucket.RefTarget("branch", "main", ""), CronPattern: fmt.Sprint(i)})
	}
	f.schedules["repo-a"][100].CronPattern = "0 0 2 * * ? *"
	f.schedules["repo-a"][100].Target.Selector = &bitbucket.Selector{Type: "default"}
	cs := connectApproving(t, f)
	out, failed := call(t, cs, "set_variable", map[string]any{"scope": "repository", "repo": "repo-a", "name": "VAR_100", "value": "new"})
	if !failed || !strings.Contains(out, "already exists") {
		t.Fatal(out)
	}
	out = mustCall(t, cs, "add_schedule", map[string]any{"repo": "repo-a", "branch": "main", "pipeline": "default", "cron": "0 0 2 * * ? *"})
	if !strings.Contains(out, "already_exists") || len(f.mutationList()) != 0 {
		t.Fatal(out)
	}
}
