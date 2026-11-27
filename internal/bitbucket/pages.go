package bitbucket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	maxPageBytes             = 4 << 20
	maxPaginationCursorBytes = 16 << 10
	maxCollectionPages       = 20
	collectionPageSize       = 100
)

// Page is a bounded part of a collection. Total is unknown when the API omits its size.
type Page[T any] struct {
	Values []T
	Total  *int
	More   bool
}

type apiPage[T any] struct {
	Values []T    `json:"values"`
	Size   *int   `json:"size"`
	Next   string `json:"next"`
}

// readPage never follows next. Callers choose a bounded number of pages to read.
func readPage[T any](ctx context.Context, c *Client, rawURL string, limit int) (apiPage[T], error) {
	if err := c.checkProjectURL(ctx, rawURL); err != nil {
		return apiPage[T]{}, err
	}
	return readWorkspacePage[T](ctx, c, rawURL, limit)
}

// readWorkspacePage reads a page of a workspace-wide listing without the project check, which
// needs one repository in the URL: the caller must narrow the request and keep only the values
// the server policy allows.
func readWorkspacePage[T any](ctx context.Context, c *Client, rawURL string, limit int) (apiPage[T], error) {
	var page apiPage[T]
	if !inAPI(rawURL) {
		return page, fmt.Errorf("unexpected pagination link outside the Bitbucket API")
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return page, err
	}
	resp, err := c.getUnchecked(ctx, rawURL, c.basicAuth, nil)
	if err != nil {
		return page, fmt.Errorf("reading Bitbucket page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return page, newAPIError(http.MethodGet, target.Path, resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil || len(data) > maxPageBytes {
		return page, errors.New("bitbucket collection page is unreadable or exceeds 4 MiB")
	}
	if err = json.Unmarshal(data, &page); err != nil {
		return apiPage[T]{}, fmt.Errorf("reading bounded Bitbucket page: %w", err)
	}
	if len(page.Values) > limit || page.Size != nil && *page.Size < 0 || len(page.Values) == 0 && page.Next != "" {
		return apiPage[T]{}, fmt.Errorf("bitbucket returned an invalid or oversized collection page")
	}
	return page, nil
}

// list returns a complete collection or an error. Callers use absence to decide whether to
// create an object, so a partial collection must never look complete.
func list[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	err := walkPages(ctx, c, path, func(values []T) bool {
		all = append(all, values...)
		return true
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

func walkPages[T any](ctx context.Context, c *Client, path string, visit func([]T) bool) error {
	first := fmt.Sprintf("%s%s?pagelen=%d", APIURL, path, collectionPageSize)
	target, err := url.Parse(first)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for next := first; next != ""; {
		if err = ctx.Err(); err != nil {
			return err
		}
		if len(seen) == maxCollectionPages {
			return fmt.Errorf("bitbucket collection exceeds %d pages", maxCollectionPages)
		}
		if len(next) > maxPaginationCursorBytes || !inAPI(next) {
			return errors.New("invalid Bitbucket pagination URL")
		}
		cursor, err := url.Parse(next)
		if err != nil || !samePageCollection(next, cursor, target) {
			return errors.New("pagination URL must stay on the requested Bitbucket collection")
		}
		if seen[next] {
			return errors.New("bitbucket repeated a collection pagination URL")
		}
		seen[next] = true
		page, err := readPage[T](ctx, c, next, collectionPageSize)
		if err != nil {
			return err
		}
		if !visit(page.Values) {
			return nil
		}
		next = page.Next
	}
	return nil
}

func samePageCollection(rawURL string, next, target *url.URL) bool {
	return next.Scheme == target.Scheme && next.Host == target.Host && next.User == nil && !strings.Contains(rawURL, "#") &&
		strings.TrimSuffix(next.EscapedPath(), "/") == strings.TrimSuffix(target.EscapedPath(), "/")
}
