package tools

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStopPipelineConfirmsResolvedUUID(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, version string) {
		f := newFake()
		p := f.addRun("repo-a", running())
		u := &fakeUser{pages: f.pages, action: "accept", values: url.Values{}}
		cs := connectWith(t, f, clientOptions(u.elicit), version)
		out := mustCall(t, cs, "stop_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1"})
		if !strings.Contains(out, `"stop_requested"`) || !strings.Contains(f.pages.openedPage(), p.UUID) {
			t.Fatal(out)
		}
		got := f.mutationList()
		if len(got) != 1 || got[0] != "POST /2.0/repositories/ws/repo-a/pipelines/"+p.UUID+"/stopPipeline" {
			t.Fatalf("writes = %v", got)
		}
	})
}

func TestStopPipelineRequiresApprovalAndHonorsPolicies(t *testing.T) {
	for _, policy := range []Policy{{}, {Repositories: []string{}}, {Tools: []string{"get_pipeline"}}, {Projects: []string{"OTHER"}}} {
		f := newFake()
		f.addRun("repo-a", running())
		cs := connectClient(t, newTestServer(t, f, policy), clientOptions(nil), "")
		if _, failed := call(t, cs, "stop_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1"}); !failed || len(f.mutationList()) != 0 {
			t.Fatal("stopped a pipeline without authorization")
		}
	}
	f := newFake()
	f.addRun("repo-a", running())
	u := &fakeUser{pages: f.pages, action: "decline"}
	out := mustCall(t, connect(t, f, u.elicit), "stop_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1"})
	if !strings.Contains(out, "cancelled") || len(f.mutationList()) != 0 {
		t.Fatal(out)
	}
}

func TestStopPipelineAutomaticCompletionAndValidation(t *testing.T) {
	for _, finished := range []bool{false, true} {
		f := newFake()
		p := f.addRun("repo-a", running())
		if finished {
			p.State = completed("SUCCESSFUL")
		}
		cs := connectClient(t, newTestServer(t, f, Policy{ConfirmationTools: []string{"run_pipeline"}}), clientOptions(nil), "")
		out := mustCall(t, cs, "stop_pipeline", map[string]any{"repo": "repo-a", "pipeline": strings.Trim(p.UUID, "{}")})
		if finished && !strings.Contains(out, "already_completed") || !finished && !strings.Contains(out, "stop_requested") || f.pages.served() {
			t.Fatal(out)
		}
		want := 1
		if finished {
			want = 0
		}
		if len(f.mutationList()) != want {
			t.Fatal(f.mutationList())
		}
	}
	f := newFake()
	cs := connectApproving(t, f)
	for _, id := range []string{"", "../1", "not-a-uuid", "999"} {
		if _, failed := call(t, cs, "stop_pipeline", map[string]any{"repo": "repo-a", "pipeline": id}); !failed {
			t.Fatalf("accepted %q", id)
		}
	}
	if len(f.mutationList()) != 0 || f.pages.served() {
		t.Fatal("invalid pipeline reached approval or stop")
	}
}

func TestStopPipelineProjectMoveAndTargetChange(t *testing.T) {
	for _, moveProject := range []bool{false, true} {
		f := newFake()
		p := f.addRun("repo-a", running())
		f.projects = map[string]string{"repo-a": "APP"}
		u := &fakeUser{pages: f.pages, action: "accept", values: url.Values{}}
		cs := connectClient(t, newTestServer(t, f, Policy{Projects: []string{"APP"}}), clientOptions(func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			f.mu.Lock()
			if moveProject {
				f.projects["repo-a"] = "OTHER"
			} else {
				p.UUID = "{ffffffff-ffff-4fff-8fff-ffffffffffff}"
			}
			f.mu.Unlock()
			return u.elicit(ctx, req)
		}), "")
		if _, failed := call(t, cs, "stop_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1"}); !failed || len(f.mutationList()) != 0 {
			t.Fatal("changed target was stopped")
		}
	}
}

func TestStopPipelinePropagatesStopError(t *testing.T) {
	for _, stopError := range []bool{false, true} {
		f := newFake()
		f.addRun("repo-a", running())
		if stopError {
			f.pipelineStopStatus = http.StatusTooManyRequests
		} else {
			f.pipelineGetStatus = http.StatusForbidden
		}
		cs := connectClient(t, newTestServer(t, f, Policy{ConfirmationTools: []string{"run_pipeline"}}), clientOptions(nil), "")
		out, failed := call(t, cs, "stop_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1"})
		want := "403"
		if stopError {
			want = "429"
		}
		if !failed || !strings.Contains(out, want) {
			t.Fatal(out)
		}
	}
}
