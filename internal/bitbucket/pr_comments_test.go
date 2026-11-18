package bitbucket

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCreatePullRequestCommentPayloadAndAuth(t *testing.T) {
	for _, parent := range []int{0, 11} {
		t.Run(fmt.Sprint(parent), func(t *testing.T) {
			requests := 0
			content := "  Reply **Markdown**\n<script>text</script>\n"
			c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodPost || r.URL.Path != "/2.0/repositories/ws/repo-a/pullrequests/7/comments" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected write: %s %s", r.Method, r.URL)
				}
				if user, token, ok := r.BasicAuth(); !ok || user != testEmail || token != testToken {
					t.Error("write did not use shared auth")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				var raw commentContent
				if err := json.Unmarshal(body["content"], &raw); err != nil || raw.Raw != content {
					t.Fatalf("raw Markdown changed: %+v, %v", raw, err)
				}
				if parent == 0 {
					if len(body) != 1 {
						t.Fatalf("general comment has extra fields: %s", body)
					}
				} else if string(body["parent"]) != `{"id":11}` || len(body) != 2 {
					t.Fatalf("reply parent missing or extra fields: %s", body)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = fmt.Fprint(w, `{"id":101,"links":{"html":{"href":"https://evil.invalid"}}}`)
			})
			out, err := c.CreatePullRequestComment(t.Context(), "repo-a", 7, content, parent)
			if err != nil || out.ID != 101 || requests != 1 {
				t.Fatalf("create: %+v, requests=%d, err=%v", out, requests, err)
			}
		})
	}
}

func TestGetPullRequestCommentUsesPRRoute(t *testing.T) {
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/2.0/repositories/ws/repo-a/pullrequests/7/comments/11" {
			t.Errorf("wrong parent lookup: %s %s", r.Method, r.URL)
		}
		_, _ = fmt.Fprint(w, `{"id":11,"content":{"raw":"parent"},"deleted":true}`)
	})
	comment, err := c.GetPullRequestComment(t.Context(), "repo-a", 7, 11)
	if err != nil || comment.ID != 11 || !comment.Deleted || comment.Content.Raw != "parent" {
		t.Fatalf("parent lookup: %+v, %v", comment, err)
	}
}

func TestCommentWritesValidateAndDoNotRetryAPIErrors(t *testing.T) {
	c := newTestClient(func(http.ResponseWriter, *http.Request) { t.Error("invalid write reached API") })
	for _, args := range []struct {
		id, parent int
		content    string
	}{
		{0, 0, "text"}, {7, -1, "text"}, {7, 0, " \t\n"}, {7, 0, strings.Repeat("x", MaxCommentBytes+1)},
	} {
		if _, err := c.CreatePullRequestComment(t.Context(), "repo-a", args.id, args.content, args.parent); err == nil {
			t.Fatal("accepted invalid comment write")
		}
	}
	if _, err := c.GetPullRequestComment(t.Context(), "repo-a", 7, 0); err == nil {
		t.Fatal("accepted invalid parent ID")
	}
	for _, status := range []int{403, 404, 429, 503} {
		requests := 0
		c = newTestClient(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			w.Header().Set("Retry-After", "17")
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, `{"error":{"message":"cannot comment"}}`)
		})
		_, err := c.CreatePullRequestComment(t.Context(), "repo-a", 7, "text", 0)
		var apiErr *APIError
		if requests != 1 || !errors.As(err, &apiErr) || apiErr.Status != status || apiErr.Message != "cannot comment" || status == 429 && apiErr.RetryAfter <= 0 {
			t.Fatalf("write error lost or retried: requests=%d, err=%v", requests, err)
		}
	}
}

func TestPullRequestCommentPageUsesSharedAuthAndIgnoresNext(t *testing.T) {
	requests := 0
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/2.0/repositories/ws/repo-a/pullrequests/7/comments" || r.URL.RawQuery != "page=3&pagelen=2&sort=created_on" {
			t.Errorf("unexpected comment request: %s %s", r.Method, r.URL)
		}
		if user, token, ok := r.BasicAuth(); !ok || user != testEmail || token != testToken {
			t.Error("comment request did not use shared authentication")
		}
		_, _ = fmt.Fprint(w, `{"values":[{"id":11,"user":{"display_name":"Ann"},"content":{"raw":"Review **this**"},"parent":{"id":3},"inline":{"path":"a.go","from":null,"to":8,"start_to":6},"resolution":{"user":{"display_name":"Bob"},"created_on":"resolved"},"pending":true}],"size":5,"next":"https://evil.invalid/steal"}`)
	})
	page, err := c.ListPullRequestComments(t.Context(), "repo-a", 7, 3, 2)
	if err != nil || requests != 1 || !page.More || page.Total == nil || *page.Total != 5 || len(page.Values) != 1 {
		t.Fatalf("comment page: %+v, requests=%d, err=%v", page, requests, err)
	}
	comment := page.Values[0]
	if comment.ID != 11 || comment.Content.Raw != "Review **this**" || comment.User.DisplayName != "Ann" || comment.Parent == nil || comment.Parent.ID != 3 || comment.Inline == nil || comment.Inline.From != nil || comment.Inline.To == nil || *comment.Inline.To != 8 || comment.Inline.StartTo == nil || *comment.Inline.StartTo != 6 || comment.Resolution == nil || comment.Resolution.User.DisplayName != "Bob" || !comment.Pending {
		t.Fatalf("comment metadata lost: %+v", comment)
	}
}

func TestPullRequestCommentsRejectInvalidOrOversizedPages(t *testing.T) {
	for _, body := range []string{
		`{"values":[{"id":1},{"id":2}]}`,
		`{"values":[],"next":"https://api.bitbucket.org/repeat"}`,
		`{"values":[{"id":1,"content":{"raw":"` + strings.Repeat("x", maxPageBytes) + `"}}]}`,
		`{"values":[`,
	} {
		c := newTestClient(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) })
		if _, err := c.ListPullRequestComments(t.Context(), "repo-a", 7, 1, 1); err == nil {
			t.Fatal("accepted an invalid or unbounded comment page")
		}
	}
}

func TestPullRequestCommentsReturnsAPIErrorAndRateLimit(t *testing.T) {
	for _, status := range []int{403, 404, 429} {
		c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "17")
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, `{"error":{"message":"comments unavailable"}}`)
		})
		_, err := c.ListPullRequestComments(t.Context(), "repo-a", 7, 1, 10)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != status || apiErr.Message != "comments unavailable" || status == 429 && apiErr.RetryAfter <= 0 {
			t.Fatalf("comment API error was lost: %v", err)
		}
	}
}

func TestPullRequestCommentsValidateBeforeRequest(t *testing.T) {
	c := newTestClient(func(http.ResponseWriter, *http.Request) { t.Error("invalid input reached Bitbucket") })
	for _, args := range [][3]int{{0, 1, 10}, {-1, 1, 10}, {7, 0, 10}, {7, 1, 0}, {7, 1, 51}} {
		if _, err := c.ListPullRequestComments(t.Context(), "repo-a", args[0], args[1], args[2]); err == nil {
			t.Fatal("invalid comment request accepted")
		}
	}
}
