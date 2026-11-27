package bitbucket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCollectionRejectsUnboundedOrCrossCollectionPagination(t *testing.T) {
	const path = "/repositories/ws/repo-a/deploy-keys"
	for _, tc := range []struct {
		name  string
		page  func(int) string
		calls int
		want  string
	}{
		{"foreign collection", func(int) string {
			return `{"values":[{}],"next":"` + APIURL + `/repositories/ws/repo-b/deploy-keys?page=2"}`
		}, 1, "requested Bitbucket collection"},
		{"repeated cursor", func(int) string {
			return `{"values":[{}],"next":"` + APIURL + path + `?pagelen=100"}`
		}, 1, "repeated a collection pagination URL"},
		{"too many pages", func(n int) string {
			return fmt.Sprintf(`{"values":[{}],"next":%q}`, APIURL+path+"?page="+strconv.Itoa(n+1))
		}, maxCollectionPages, "collection exceeds"},
		{"too many records", func(int) string {
			return `{"values":[` + strings.Repeat("{},", collectionPageSize) + `{}]}`
		}, 1, "oversized collection page"},
		{"oversized body", func(int) string {
			return `{"values":[{}]}` + strings.Repeat(" ", maxPageBytes)
		}, 1, "exceeds 4 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls > tc.calls {
					t.Error("requested another page after the limit")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = fmt.Fprint(w, tc.page(calls))
			})
			keys, err := c.ListDeployKeys(t.Context(), "repo-a")
			if keys != nil || err == nil || !strings.Contains(err.Error(), tc.want) || calls != tc.calls {
				t.Fatalf("keys=%v, error=%v, requests=%d", keys, err, calls)
			}
		})
	}
}

func TestFindVariableStopsOnMatchingPage(t *testing.T) {
	const path = "/repositories/ws/repo-a/pipelines_config/variables"
	calls := 0
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case r.URL.RawQuery == "pagelen=100" && calls == 1:
			_, _ = fmt.Fprintf(w, `{"values":[{"key":"OTHER"}],"next":%q}`, APIURL+path+"?cursor=opaque%2Bcursor")
		case r.URL.RawQuery == "cursor=opaque%2Bcursor" && calls == 2:
			_, _ = fmt.Fprintf(w, `{"values":[{"key":"TOKEN","uuid":"{match}"}],"next":%q}`, APIURL+path+"?cursor=unused")
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	v, err := c.FindVariable(t.Context(), VariableScope{Repo: "repo-a"}, "TOKEN")
	if err != nil || v == nil || v.UUID != "{match}" || calls != 2 {
		t.Fatalf("variable=%v, error=%v, requests=%d", v, err, calls)
	}
}

func TestPullRequestStatusesDoesNotFollowNext(t *testing.T) {
	for _, knownTotal := range []bool{false, true} {
		t.Run(fmt.Sprint(knownTotal), func(t *testing.T) {
			requests := 0
			c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Query().Get("pagelen") != "50" {
					t.Error("request did not limit upstream records")
				}
				// Bitbucket lists the oldest first; the page must hold the newest.
				if r.URL.Query().Get("sort") != "-created_on" {
					t.Error("request did not ask for the newest results first")
				}
				page := map[string]any{"values": make([]CommitStatus, 50), "next": APIURL + r.URL.EscapedPath()[len("/2.0"):] + "?page=2"}
				if knownTotal {
					page["size"] = 500_000
				}
				_ = json.NewEncoder(w).Encode(page)
			})
			page, err := c.PullRequestStatuses(t.Context(), "repo-a", 7, 1, 50)
			if err != nil || len(page.Values) != 50 || !page.More || requests != 1 {
				t.Fatalf("page=%+v, requests=%d, err=%v", page, requests, err)
			}
			if knownTotal && (page.Total == nil || *page.Total != 500_000) || !knownTotal && page.Total != nil {
				t.Fatal("incorrect reported total")
			}
		})
	}
}

func TestPullRequestDiffStatSkipsUnrequestedPages(t *testing.T) {
	var pages []int
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pullrequests/") {
			w.Header().Set("Location", strings.Replace(diffLocation, "/diff/", "/diffstat/", 1))
			w.WriteHeader(http.StatusFound)
			return
		}
		n, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || r.URL.Query().Get("pagelen") != "100" || r.URL.Query().Get("topic") != "true" {
			t.Fatalf("incorrect upstream page: %s", r.URL.RawQuery)
		}
		pages = append(pages, n)
		values := make([]DiffStat, 100)
		for i := range values {
			values[i].New = &CommitFile{Path: fmt.Sprint((n-1)*100 + i)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"values": values, "size": 100_000, "next": APIURL + r.URL.EscapedPath()[len("/2.0"):] + "?pagelen=100&topic=true&page=" + strconv.Itoa(n+1),
		})
	})
	got, err := c.PullRequestDiffStat(t.Context(), testDiffSnapshot(t, c, "diffstat"), 20_000, 200)
	if err != nil || len(got.Values) != 200 || !got.More || got.Total == nil || *got.Total != 100_000 || !reflect.DeepEqual(pages, []int{201, 202}) {
		t.Fatalf("pages=%v, result=%+v, err=%v", pages, got, err)
	}
	if got.Values[0].New.Path != "20000" || got.Values[199].New.Path != "20199" {
		t.Fatal("incorrect requested range")
	}
}

func TestCollectionPagesRejectOversizedOrEmptyNext(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"too many records", `{"values":[` + strings.Repeat(`{},`, 50) + `{}]}`},
		{"too many bytes", `{"values":[{"name":"` + strings.Repeat("x", maxPageBytes) + `"}]}`},
		{"empty continuing page", `{"values":[],"next":"` + APIURL + `/repeated"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, tc.body) })
			if _, err := c.PullRequestStatuses(t.Context(), "repo-a", 7, 1, 50); err == nil {
				t.Fatal("unbounded or non-progressing response accepted")
			}
		})
	}
}

func TestDiffStatCannotTraverseUnboundedSmallPages(t *testing.T) {
	reads := 0
	c := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pullrequests/") {
			w.Header().Set("Location", strings.Replace(diffLocation, "/diff/", "/diffstat/", 1))
			w.WriteHeader(http.StatusFound)
			return
		}
		reads++
		_ = json.NewEncoder(w).Encode(map[string]any{"values": []DiffStat{{Status: "added"}}, "next": APIURL + r.URL.EscapedPath()[len("/2.0"):] + "?page=next"})
	})
	if _, err := c.PullRequestDiffStat(t.Context(), testDiffSnapshot(t, c, "diffstat"), 0, 200); err == nil || reads != 3 {
		t.Fatalf("unbounded traversal: reads=%d err=%v", reads, err)
	}
}
