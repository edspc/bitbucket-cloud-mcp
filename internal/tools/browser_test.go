package tools

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLocalBrowserApprovesChangesWithoutClientElicitation(t *testing.T) {
	for _, tc := range changeCases() {
		t.Run(tc.name, func(t *testing.T) {
			forEachProtocol(t, func(t *testing.T, version string) {
				f := approvalFake()
				server := newTestServer(t, f, Policy{LocalBrowser: true})
				opened := 0
				f.tools.openBrowser = func(_ context.Context, target string) error {
					opened++
					if len(f.mutationList()) != 0 {
						t.Error("change happened before browser approval")
					}
					if status := f.pages.submit(target, url.Values{"NEW": {"secret-marker"}, "TOKEN": {"secret-marker"}, tokenField: {f.validToken}}); status != http.StatusSeeOther {
						t.Errorf("browser submission returned %d", status)
						return errors.New("browser submission failed")
					}
					return nil
				}
				// Model the desktop rejecting URL elicitation immediately. Local mode must not ask it.
				opts := clientOptions(func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					t.Error("client URL elicitation was used")
					return &mcp.ElicitResult{Action: "cancel"}, nil
				})
				cs := connectClient(t, server, opts, version)
				out := mustCall(t, cs, tc.name, tc.args)
				if opened != 1 || strings.Contains(out, "secret-marker") || strings.Contains(out, f.validToken) {
					t.Fatalf("local approval failed or leaked a secret: %s", out)
				}
				if len(f.mutationList()) == 0 {
					t.Fatal("approved operation did not run")
				}
				expectPagesClosed(t, f, 1)
			})
		})
	}
}

func TestLocalBrowserStillRequiresFormSubmission(t *testing.T) {
	for _, tc := range changeCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := approvalFake()
			server := newTestServer(t, f, Policy{LocalBrowser: true})
			f.tools.secretWait = 150 * time.Millisecond
			f.tools.openBrowser = func(context.Context, string) error { return nil }
			cs := connectClient(t, server, clientOptions(nil), "")
			out, failed := call(t, cs, tc.name, tc.args)
			if !failed || !strings.Contains(out, "nothing was submitted") || len(f.mutationList()) != 0 {
				t.Fatalf("opening the browser authorized a write: %s", out)
			}
			expectPagesClosed(t, f, 1)
		})
	}
}

func TestLocalBrowserOpenFailureClosesPendingPage(t *testing.T) {
	f := approvalFake()
	server := newTestServer(t, f, Policy{LocalBrowser: true})
	f.tools.openBrowser = func(context.Context, string) error { return errors.New("browser unavailable") }
	cs := connectClient(t, server, clientOptions(nil), "")
	out, failed := call(t, cs, "set_variable", map[string]any{"scope": "repository", "repo": "repo-b", "name": "SECRET", "secured": true})
	if !failed || !strings.Contains(out, "cannot open the system browser") || len(f.mutationList()) != 0 {
		t.Fatalf("browser error did not stop write: %s", out)
	}
	expectPagesClosed(t, f, 1)
	f.tools.mu.Lock()
	defer f.tools.mu.Unlock()
	if len(f.tools.pending) != 0 || f.tools.openPages != 0 {
		t.Fatal("browser error left an active page")
	}
}

func TestLocalBrowserHonorsReadConfirmationAndAccessLimits(t *testing.T) {
	for _, denied := range []bool{false, true} {
		f := approvalFake()
		repos := []string{"repo-a"}
		if denied {
			repos = []string{}
		}
		server := newTestServer(t, f, Policy{LocalBrowser: true, Repositories: repos, ConfirmationTools: []string{"list_pipelines"}})
		opened := 0
		f.tools.openBrowser = func(_ context.Context, target string) error {
			opened++
			if status := f.pages.submit(target, nil); status != http.StatusSeeOther {
				t.Errorf("browser submission returned %d", status)
				return errors.New("browser submission failed")
			}
			return nil
		}
		cs := connectClient(t, server, clientOptions(nil), "")
		out, failed := call(t, cs, "list_pipelines", map[string]any{"repo": "repo-a"})
		wantOpened := 1
		if denied {
			wantOpened = 0
		}
		if failed != denied || opened != wantOpened {
			t.Fatalf("local read policy: %s, opened=%d", out, opened)
		}
	}
}

func TestLocalBrowserRechecksVariableDestination(t *testing.T) {
	f := approvalFake()
	server := newTestServer(t, f, Policy{LocalBrowser: true})
	f.tools.openBrowser = func(_ context.Context, target string) error {
		if status := f.pages.submit(target, url.Values{"TOKEN": {"secret-marker"}}); status != http.StatusSeeOther {
			t.Errorf("browser submission returned %d", status)
			return errors.New("browser submission failed")
		}
		f.mu.Lock()
		f.envs["repo-b"] = []bitbucket.Environment{{Name: "Production", UUID: "{replacement}"}}
		f.mu.Unlock()
		return nil
	}
	cs := connectClient(t, server, clientOptions(nil), "")
	out, failed := call(t, cs, "set_variable", map[string]any{"scope": "deployment", "repo": "repo-b", "environment": "Production", "name": "TOKEN", "secured": true})
	if !failed || !strings.Contains(out, "changed while awaiting approval") || len(f.mutationList()) != 0 {
		t.Fatalf("changed destination accepted: %s", out)
	}
	expectPagesClosed(t, f, 1)
}

func TestLocalBrowserCancellationClosesPage(t *testing.T) {
	f := approvalFake()
	server := newTestServer(t, f, Policy{LocalBrowser: true})
	f.tools.secretWait = time.Minute
	opened := make(chan string, 1)
	f.tools.openBrowser = func(_ context.Context, target string) error { opened <- target; return nil }
	cs := connectClient(t, server, clientOptions(nil), "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "set_variable", Arguments: map[string]any{"scope": "repository", "repo": "repo-b", "name": "SECRET", "secured": true}})
	}()
	var target string
	select {
	case target = <-opened:
	case <-time.After(time.Second):
		t.Fatal("browser did not open")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled call did not return")
	}
	eventually(t, func() bool { return f.pages.stopCount() == 1 })
	if len(f.mutationList()) != 0 || f.pages.submit(target, url.Values{"SECRET": {"late-secret"}}) != http.StatusGone {
		t.Fatal("cancelled approval was still usable")
	}
}
