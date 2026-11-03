package bitbucket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAPIRequestMethodsQueriesAndAuth(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
			user, token, ok := r.BasicAuth()
			if !ok || user != testEmail || token != testToken || r.Method != method || r.URL.Host != "api.bitbucket.org" || r.URL.Path != "/2.0/repositories/ws/repo-a/issues" {
				t.Fatal("request lost its target, method or configured auth")
			}
			if got := r.URL.Query()["include"]; len(got) != 2 || got[0] != "one" || got[1] != "two" || r.URL.Query().Get("q") != "title ~ \"hello\"" {
				t.Fatal(r.URL)
			}
			if method == "POST" || method == "PUT" || method == "PATCH" {
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["title"] != "hello" || r.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("body: %v %v", body, err)
				}
			}
			w.WriteHeader(http.StatusNoContent)
		})
		in := APIRequest{Method: method, Path: "/repositories/ws/repo-a/issues?include=one", Query: map[string][]string{"include": {"two"}, "q": {`title ~ "hello"`}}}
		if method == "POST" || method == "PUT" || method == "PATCH" {
			in.Body = map[string]any{"title": "hello"}
		}
		out, err := c.DoAPIRequest(t.Context(), in)
		if err != nil || out.Status != 204 || out.Body != nil || out.Text != "" {
			t.Fatalf("%s: %+v %v", method, out, err)
		}
	}
}

func TestAPIRequestRejectsUnsafeInputsWithoutRequests(t *testing.T) {
	c := newTestClient(func(http.ResponseWriter, *http.Request) { t.Error("invalid request reached transport") })
	for _, in := range []APIRequest{
		{Method: "TRACE", Path: "/user"}, {Method: "get", Path: "/user"},
		{Method: "GET", Path: "https://evil.example/collect"}, {Method: "GET", Path: "//evil.example/collect"},
		{Method: "GET", Path: "/user#fragment"}, {Method: "GET", Path: "/user/../repositories/other/repo"},
		{Method: "GET", Path: "/user/%2e%2e/repo"}, {Method: "GET", Path: "/user/%252e%252e/repo"},
		{Method: "GET", Path: "/user\\other"}, {Method: "GET", Path: "/user//other"},
		{Method: "GET", Path: "/repositories/other/repo"}, {Method: "GET", Path: "/workspaces/other"},
		{Method: "GET", Path: "/snippets/other"}, {Method: "DELETE", Path: "/snippets/other/id"},
		{Method: "GET", Path: "/repositories"}, {Method: "GET", Path: "/2.0/user"},
		{Method: "GET", Path: "/user?access_token=secret"}, {Method: "GET", Path: "/user?password=secret"},
		{Method: "GET", Path: "/user?x=api-secret"}, {Method: "GET", Path: "/user?x=%61pi-secret"},
		{Method: "GET", Path: "/user/api%2Dsecret"}, {Method: "GET", Path: "/user", Body: map[string]any{}},
		{Method: "POST", Path: "/user", Body: map[string]any{"access_token": "secret"}},
		{Method: "POST", Path: "/user", Body: map[string]any{"variables": []any{map[string]any{"secured": true, "value": "secret"}}}},
		{Method: "POST", Path: "/user", Body: map[string]any{"name": "api-secret"}},
		{Method: "POST", Path: "/user", Body: strings.Repeat("x", maxAPIRequestBytes)},
	} {
		if _, err := c.DoAPIRequest(t.Context(), in); err == nil {
			t.Fatalf("accepted method %s path %s", in.Method, in.Path)
		}
	}
}

func TestAPIRequestAcceptsConfiguredWorkspaceSnippets(t *testing.T) {
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2.0/snippets/ws/id" || r.Method != http.MethodGet {
			t.Fatal(r.URL)
		}
		_, _ = fmt.Fprint(w, `{"id":"id","title":"snippet"}`)
	})
	out, err := c.DoAPIRequest(t.Context(), APIRequest{Method: "GET", Path: "/snippets/ws/id"})
	if err != nil || out.Status != 200 {
		t.Fatalf("configured-workspace snippet denied: %+v %v", out, err)
	}
}

func TestAPIRequestRedactsCredentialsAndPartialVariableResponses(t *testing.T) {
	basic := base64.StdEncoding.EncodeToString([]byte(testEmail + ":" + testToken))
	c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":9007199254740993,"title":"ordinary","access_token":"response-token","nested":{"private_key":"private-key-marker"},"values":[{"key":"SECRET","secured":true,"value":"secured-marker"},{"value":"partial-marker"}],"echo":"%s %s"}`, testToken, basic)
	})
	out, err := c.DoAPIRequest(t.Context(), APIRequest{Method: "GET", Path: "/user"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testToken, basic, "response-token", "private-key-marker", "secured-marker", "partial-marker"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("response leaked %s", secret)
		}
	}
	if !strings.Contains(string(data), "9007199254740993") || !strings.Contains(string(data), "ordinary") {
		t.Fatal("ordinary response data changed")
	}
}

func TestAPIRequestBoundsErrorsRedirectsAndText(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		fails  bool
	}{
		{200, "plain text api-secret", false}, {200, "binary\x00data", true},
		{200, strings.Repeat("x", maxAPIResponseBytes+1), true},
		{307, `{"error":{"message":"redirect api-secret"}}`, true},
		{429, `{"error":{"message":"slow down api-secret"}}`, true},
		{403, `{"error":{"message":"permission denied"}}`, true},
	} {
		requests := 0
		c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			w.Header().Set("Location", "https://evil.example/collect")
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(tc.status)
			_, _ = fmt.Fprint(w, tc.body)
		})
		out, err := c.DoAPIRequest(t.Context(), APIRequest{Method: "GET", Path: "/user"})
		if (err != nil) != tc.fails || requests != 1 {
			t.Fatalf("HTTP %d: %+v %v requests %d", tc.status, out, err, requests)
		}
		if err != nil && strings.Contains(err.Error(), testToken) || strings.Contains(out.Text, testToken) {
			t.Fatal("configured credentials leaked")
		}
		if tc.status == 429 && (!IsStatus(err, 429) || !strings.Contains(err.Error(), "30")) {
			t.Fatalf("rate-limit advice lost: %v", err)
		}
	}
}

func TestAPIRequestRejectsExpiredApproval(t *testing.T) {
	c := newTestClient(func(http.ResponseWriter, *http.Request) { t.Error("expired approval reached transport") })
	ctx := WithWriteDeadline(context.Background(), time.Now().Add(-time.Second))
	if _, err := c.DoAPIRequest(ctx, APIRequest{Method: "POST", Path: "/repositories/ws/repo-a/issues"}); err == nil {
		t.Fatal("expired approval allowed a write")
	}
}

func TestAPIRequestDoesNotExposeMalformedJSONOrTransportDetails(t *testing.T) {
	c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"response-secret"} trailing garbage`)
	})
	if out, err := c.DoAPIRequest(t.Context(), APIRequest{Method: "GET", Path: "/user"}); err == nil || out.Text != "" || out.Body != nil || strings.Contains(err.Error(), "response-secret") {
		t.Fatalf("malformed JSON exposed: %+v %v", out, err)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("transport secret")} {
		c = New(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("reflected api-secret: %w", cause)
		})}, Config{Workspace: "ws", Email: testEmail, Token: testToken})
		_, err := c.DoAPIRequest(t.Context(), APIRequest{Method: "GET", Path: "/user"})
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe transport error: %v", err)
		}
		if (cause == context.Canceled || cause == context.DeadlineExceeded) && !errors.Is(err, cause) {
			t.Fatalf("cancellation lost: %v", err)
		}
	}
}

func TestAPIRequestClientCannotBypassRestrictions(t *testing.T) {
	for _, policy := range []struct{ projects, repos []string }{
		{nil, []string{}}, {nil, []string{"repo-a"}}, {[]string{}, nil}, {[]string{"APP"}, nil}, {[]string{"APP"}, []string{"repo-a"}},
	} {
		c := newTestClient(func(http.ResponseWriter, *http.Request) { t.Error("restricted proxy reached API") }).WithAllowedProjects(policy.projects, policy.repos)
		if _, err := c.DoAPIRequest(t.Context(), APIRequest{Method: "GET", Path: "/repositories/ws/repo-a"}); err == nil {
			t.Fatal("proxy bypassed client policy")
		}
	}
}
