package bitbucket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Source reads are pinned before pagination, so a moving branch cannot mix file versions.
func (c *Client) ResolveRevision(ctx context.Context, repo, revision string) (string, error) {
	if hash, err := fullSHA(revision); err == nil {
		return hash, nil
	}
	var commit Commit
	if err := c.call(ctx, http.MethodGet, c.repoPath(repo, "commit", revision), nil, &commit); err != nil {
		return "", err
	}
	return fullSHA(commit.Hash)
}

type SourceEntry struct {
	Type       string   `json:"type"`
	Path       string   `json:"path"`
	Size       int64    `json:"size"`
	Attributes []string `json:"attributes,omitempty"`
}

// sourcePath keeps file path separators, while escaping every segment separately.
func (c *Client) sourcePath(repo, commit, path string) string {
	base := c.repoPath(repo, "src", commit) + "/"
	if path == "" {
		return base
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return base + strings.Join(parts, "/")
}

func (c *Client) sourceMetadata(ctx context.Context, repo, commit, path string) (SourceEntry, error) {
	var entry SourceEntry
	path = c.sourcePath(repo, commit, path) + "?format=meta"
	resp, err := c.get(ctx, APIURL+path, c.basicAuth, nil)
	if err != nil {
		return entry, fmt.Errorf("reading Bitbucket source metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return entry, errors.New("source requires a redirect (including Git LFS); external file downloads are not supported")
	}
	if resp.StatusCode != http.StatusOK {
		return entry, newAPIError(http.MethodGet, path, resp)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, maxPageBytes)).Decode(&entry); err != nil {
		return SourceEntry{}, fmt.Errorf("reading bounded source metadata: %w", err)
	}
	return entry, nil
}

// SourceFile bounds the download even when metadata understates a file's size.
func (c *Client) SourceFile(ctx context.Context, repo, commit, path string, maxBytes int) ([]byte, error) {
	entry, err := c.sourceMetadata(ctx, repo, commit, path)
	if err != nil {
		return nil, err
	}
	if entry.Type != "commit_file" {
		return nil, errors.New("path is not a file; use list_directory for directories")
	}
	if entry.Size < 0 || entry.Size > int64(maxBytes) {
		return nil, fmt.Errorf("file exceeds the %d byte download limit", maxBytes)
	}
	path = c.sourcePath(repo, commit, path)
	resp, err := c.get(ctx, APIURL+path, c.basicAuth, nil)
	if err != nil {
		return nil, fmt.Errorf("reading Bitbucket file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, errors.New("file requires a redirect (including Git LFS); external file downloads are not supported")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newAPIError(http.MethodGet, path, resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("reading bounded Bitbucket file: %w", err)
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("file exceeds the %d byte download limit", maxBytes)
	}
	return data, nil
}

func (c *Client) ListDirectory(ctx context.Context, repo, commit, path string, page, limit int) (Page[SourceEntry], error) {
	entry, err := c.sourceMetadata(ctx, repo, commit, path)
	if err != nil {
		return Page[SourceEntry]{}, err
	}
	if entry.Type != "commit_directory" {
		return Page[SourceEntry]{}, errors.New("path is not a directory; use get_file for files")
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "pagelen": {strconv.Itoa(limit)}}
	p, err := readPage[SourceEntry](ctx, c, APIURL+c.sourcePath(repo, commit, path)+"?"+q.Encode(), limit)
	return Page[SourceEntry]{Values: p.Values, Total: p.Size, More: p.Next != ""}, err
}
