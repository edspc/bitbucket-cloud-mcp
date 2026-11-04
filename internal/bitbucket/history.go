package bitbucket

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const maxHistoryCursorBytes = 16 << 10

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

type HistoryPage struct {
	Values     []HistoryEntry
	Total      *int
	NextCursor string
}

// ListCommits reads just one page; monitoring and other internal lookups are unaffected.
func (c *Client) ListCommits(ctx context.Context, repo, revision, path string, renames bool, cursor string, limit int) (HistoryPage, error) {
	q := url.Values{"pagelen": {strconv.Itoa(limit)}}
	endpoint := c.repoPath(repo, "commits")
	if revision != "" {
		endpoint += escapedPath(revision)
	}
	if renames {
		endpoint = c.repoPath(repo, "filehistory", revision) + escapedPath(strings.Split(path, "/")...)
		q.Set("renames", "true")
	} else if path != "" {
		q.Set("path", path)
	}
	target, err := url.Parse(APIURL + endpoint + "?" + q.Encode())
	if err != nil {
		return HistoryPage{}, err
	}
	rawURL := target.String()
	if cursor != "" {
		if err = validateHistoryCursor(cursor, target); err != nil {
			return HistoryPage{}, err
		}
		rawURL = cursor
	}
	var result HistoryPage
	if renames {
		p, err := readPage[HistoryEntry](ctx, c, rawURL, limit)
		if err != nil {
			return HistoryPage{}, err
		}
		result = HistoryPage{Values: p.Values, Total: p.Size, NextCursor: p.Next}
	} else {
		p, err := readPage[HistoryCommit](ctx, c, rawURL, limit)
		if err != nil {
			return HistoryPage{}, err
		}
		result = HistoryPage{Values: make([]HistoryEntry, 0, len(p.Values)), Total: p.Size, NextCursor: p.Next}
		for _, commit := range p.Values {
			result.Values = append(result.Values, HistoryEntry{Commit: commit})
		}
	}
	if result.NextCursor != "" {
		if err = validateHistoryCursor(result.NextCursor, target); err != nil {
			return HistoryPage{}, err
		}
	}
	return result, nil
}

// Continuations stay opaque: validate their authority and selection without rebuilding them.
func validateHistoryCursor(cursor string, target *url.URL) error {
	if len(cursor) > maxHistoryCursorBytes {
		return errors.New("history cursor exceeds 16 KiB")
	}
	next, err := url.Parse(cursor)
	if err != nil || next.Scheme != target.Scheme || next.Host != target.Host || next.User != nil || strings.Contains(cursor, "#") ||
		strings.TrimSuffix(next.EscapedPath(), "/") != strings.TrimSuffix(target.EscapedPath(), "/") {
		return errors.New("history cursor must stay on the requested Bitbucket API repository, revision and endpoint")
	}
	query, err := url.ParseQuery(next.RawQuery)
	if err != nil {
		return errors.New("history cursor contains invalid query parameters")
	}
	filters := target.Query()
	for _, key := range []string{"pagelen", "path", "renames", "include", "exclude", "q", "sort", "fields"} {
		if !slices.Equal(query[key], filters[key]) {
			return errors.New("history cursor does not match the requested filters and limit")
		}
	}
	return nil
}
