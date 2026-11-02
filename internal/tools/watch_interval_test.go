package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWatchIntervalDefaultsBoundsAndProjectCost(t *testing.T) {
	for _, tc := range []struct {
		name            string
		seconds         int
		defaultInterval time.Duration
		projects        []string
		want            time.Duration
	}{
		{"default", 0, 30 * time.Second, nil, 30 * time.Second},
		{"default with projects", 0, 60 * time.Second, []string{"APP"}, 60 * time.Second},
		{"injected default", 0, 10 * time.Millisecond, nil, 10 * time.Millisecond},
		{"minimum", 1, 30 * time.Second, nil, time.Second},
		{"maximum", 60, 30 * time.Second, nil, time.Minute},
		{"custom with projects", 5, 60 * time.Second, []string{"APP"}, 10 * time.Second},
		{"custom with empty project policy", 5, 60 * time.Second, []string{}, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			watchTools := &tools{pollInterval: tc.defaultInterval, policy: Policy{Projects: tc.projects}}
			got, err := watchTools.watchInterval(tc.seconds)
			if err != nil || got != tc.want || watchTools.pollInterval != tc.defaultInterval {
				t.Fatalf("interval = %v, error = %v, shared default = %v", got, err, watchTools.pollInterval)
			}
		})
	}
}

func TestWatchInvalidIntervalFailsBeforeRequestsAndSlot(t *testing.T) {
	f := newFake()
	twoSteps(f)
	cs := connect(t, f, nil)
	for _, seconds := range []int64{-1, 61, 3601, 18446744074} {
		out, failed := call(t, cs, "watch_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1", "poll_interval_seconds": seconds})
		if !failed || !strings.Contains(out, "poll_interval_seconds must be between 1 and 60") {
			t.Fatal(out)
		}
	}
	if f.pipelineGet != 0 || f.tools.watchCalls != 0 {
		t.Fatal("invalid interval made requests or kept a polling slot")
	}
}

func TestWatchIntervalIsPerCall(t *testing.T) {
	f := newFake()
	slow := twoSteps(f)
	fast := twoSteps(f)
	first := make(chan struct{})
	f.onPipelineGet = func(n int) {
		switch n {
		case 1:
			close(first)
		case 3:
			f.setState(fast, completed("SUCCESSFUL"))
		case 4:
			f.setState(slow, completed("SUCCESSFUL"))
		}
	}
	cs := connect(t, f, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		value *mcp.CallToolResult
		err   error
	}
	done := make(chan result, 1)
	started := time.Now()
	go func() {
		value, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "watch_pipeline", Arguments: map[string]any{"repo": "repo-a", "pipeline": "1", "poll_interval_seconds": 1}})
		done <- result{value, err}
	}()
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal("watch did not make its immediate first request")
	}
	fastStarted := time.Now()
	value, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "watch_pipeline", Arguments: map[string]any{"repo": "repo-a", "pipeline": "2"}})
	if err != nil || value.IsError || time.Since(fastStarted) > 700*time.Millisecond {
		t.Fatalf("custom interval affected the default watch: %+v, %v", value, err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.value.IsError || time.Since(started) < time.Second {
			t.Fatalf("custom interval was ignored: %+v, %v", got.value, got.err)
		}
	case <-ctx.Done():
		t.Fatal("custom watch did not complete")
	}
	if f.tools.pollInterval != 10*time.Millisecond {
		t.Fatal("watch changed the subscription default")
	}
}

func TestWatchCustomIntervalStillHonorsTimeout(t *testing.T) {
	f := newFake()
	twoSteps(f)
	cs := connect(t, f, nil)
	started := time.Now()
	got := decodeResult[watchPipelineResult](t, mustCall(t, cs, "watch_pipeline", map[string]any{"repo": "repo-a", "pipeline": "1", "poll_interval_seconds": 60, "timeout_seconds": 1}))
	if got.Outcome != "timeout" || got.Pipeline == nil || f.pipelineGet != 1 || f.tools.watchCalls != 0 || time.Since(started) > 3*time.Second {
		t.Fatalf("timeout = %+v, requests = %d", got, f.pipelineGet)
	}
}

func TestWatchLogPollingAndHeartbeatUseCustomInterval(t *testing.T) {
	f := newFake()
	p := twoSteps(f)
	f.logs["{s1}"] = "build\n"
	f.onPipelineGet = func(n int) {
		if n == 2 {
			f.setState(p, completed("SUCCESSFUL"), new(completed("SUCCESSFUL")), new(completed("NOT_RUN")))
		}
	}
	var mu sync.Mutex
	var statuses []time.Time
	opts := clientOptions(nil)
	opts.ProgressNotificationHandler = func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		if !strings.HasPrefix(req.Params.Message, "{") {
			mu.Lock()
			defer mu.Unlock()
			statuses = append(statuses, time.Now())
		}
	}
	cs := connectWith(t, f, opts, "")
	params := &mcp.CallToolParams{Name: "watch_pipeline", Arguments: map[string]any{"repo": "repo-a", "pipeline": "1", "poll_interval_seconds": 1, "stream_logs": true, "timeout_seconds": 4}}
	params.SetProgressToken("custom-interval")
	started := time.Now()
	res, err := cs.CallTool(t.Context(), params)
	if err != nil || res.IsError || time.Since(started) < 1500*time.Millisecond {
		t.Fatalf("log polling ignored its custom interval: %+v, %v", res, err)
	}
	got := decodeResult[watchPipelineResult](t, res.Content[0].(*mcp.TextContent).Text)
	if got.Outcome != "completed" || len(got.Logs) != 1 {
		t.Fatalf("watch = %+v", got)
	}
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(statuses) >= 3 })
	mu.Lock()
	defer mu.Unlock()
	if len(statuses) > 4 || statuses[1].Sub(statuses[0]) < 900*time.Millisecond {
		t.Fatalf("heartbeat did not use the custom base interval: %v", statuses)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pipelineGet != 2 || len(f.logRanges) != 2 {
		t.Fatalf("heartbeats made extra requests: polls=%d, logs=%d", f.pipelineGet, len(f.logRanges))
	}
}
