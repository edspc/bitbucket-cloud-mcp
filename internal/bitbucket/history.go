package bitbucket

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// HistoryCommit keeps the fields needed to choose and read a past revision.
type HistoryCommit struct {
	Hash    string `json:"hash"`
	Date    string `json:"date"`
	Message string `json:"message"`
	Author  struct {
		Raw  string  `json:"raw"`
		User Account `json:"user"`
	} `json:"author"`
	Parents []Commit `json:"parents"`
}

type HistoryEntry struct {
	Commit HistoryCommit `json:"commit"`
	Path   string        `json:"path,omitempty"`
}

// ListCommits reads just one page; monitoring and other internal lookups are unaffected.
func (c *Client) ListCommits(ctx context.Context, repo, revision, path string, renames bool, page, limit int) (Page[HistoryEntry], error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "pagelen": {strconv.Itoa(limit)}}
	endpoint := c.repoPath(repo, "commits")
	if revision != "" {
		endpoint += escapedPath(revision)
	}
	if renames {
		endpoint = c.repoPath(repo, "filehistory", revision) + escapedPath(strings.Split(path, "/")...)
		q.Set("renames", "true")
		p, err := readPage[HistoryEntry](ctx, c, APIURL+endpoint+"?"+q.Encode(), limit)
		return Page[HistoryEntry]{Values: p.Values, Total: p.Size, More: p.Next != ""}, err
	}
	if path != "" {
		q.Set("path", path)
	}
	p, err := readPage[HistoryCommit](ctx, c, APIURL+endpoint+"?"+q.Encode(), limit)
	if err != nil {
		return Page[HistoryEntry]{}, err
	}
	entries := make([]HistoryEntry, 0, len(p.Values))
	for _, commit := range p.Values {
		entries = append(entries, HistoryEntry{Commit: commit})
	}
	return Page[HistoryEntry]{Values: entries, Total: p.Size, More: p.Next != ""}, nil
}
