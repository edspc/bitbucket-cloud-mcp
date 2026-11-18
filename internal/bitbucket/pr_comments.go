package bitbucket

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// CommentInline locates an inline comment on the old (from) or new (to) diff side.
// Null line numbers are distinct from a line number returned by Bitbucket.
type CommentInline struct {
	Path      string `json:"path"`
	From      *int   `json:"from,omitempty"`
	To        *int   `json:"to,omitempty"`
	StartFrom *int   `json:"start_from,omitempty"`
	StartTo   *int   `json:"start_to,omitempty"`
}

// MaxCommentBytes bounds both the write payload and the browser confirmation.
const MaxCommentBytes = 32 * 1024

type commentContent struct {
	Raw string `json:"raw"`
}

type commentParent struct {
	ID int `json:"id"`
}

func (c *Client) GetPullRequestComment(ctx context.Context, repo string, id, comment int) (PullRequestComment, error) {
	var out PullRequestComment
	if id < 1 || comment < 1 {
		return out, errors.New("pull request and comment IDs must be positive")
	}
	err := c.call(ctx, "GET", c.pullRequestPath(repo, id, "comments", strconv.Itoa(comment)), nil, &out)
	return out, err
}

// CreatePullRequestComment posts once. A positive parent creates a reply in that thread.
func (c *Client) CreatePullRequestComment(ctx context.Context, repo string, id int, content string, parent int) (PullRequestComment, error) {
	var out PullRequestComment
	if id < 1 || parent < 0 || strings.TrimSpace(content) == "" || len(content) > MaxCommentBytes {
		return out, errors.New("a positive pull request ID, nonnegative parent ID and nonblank content of at most 32 KiB are required")
	}
	in := struct {
		Content commentContent `json:"content"`
		Parent  *commentParent `json:"parent,omitempty"`
	}{Content: commentContent{Raw: content}}
	if parent > 0 {
		in.Parent = &commentParent{ID: parent}
	}
	err := c.call(ctx, "POST", c.pullRequestPath(repo, id, "comments"), in, &out)
	return out, err
}

type PullRequestComment struct {
	ID      int `json:"id"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	User      Account `json:"user"`
	CreatedOn string  `json:"created_on"`
	UpdatedOn string  `json:"updated_on"`
	Parent    *struct {
		ID int `json:"id"`
	} `json:"parent"`
	Inline     *CommentInline `json:"inline"`
	Deleted    bool           `json:"deleted"`
	Pending    bool           `json:"pending"`
	Resolution *struct {
		User      Account `json:"user"`
		CreatedOn string  `json:"created_on"`
	} `json:"resolution"`
}

// ListPullRequestComments reads one bounded page, including replies and inline comments.
func (c *Client) ListPullRequestComments(ctx context.Context, repo string, id, page, limit int) (Page[PullRequestComment], error) {
	if id < 1 || page < 1 || limit < 1 || limit > 50 {
		return Page[PullRequestComment]{}, errors.New("pull request and page must be positive; comment limit must be between 1 and 50")
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "pagelen": {strconv.Itoa(limit)}, "sort": {"created_on"}}
	p, err := readPage[PullRequestComment](ctx, c, APIURL+c.pullRequestPath(repo, id, "comments")+"?"+q.Encode(), limit)
	return Page[PullRequestComment]{Values: p.Values, Total: p.Size, More: p.Next != ""}, err
}

func (c *Client) PullRequestCommentURL(repo string, id, comment int) string {
	return c.PullRequestURL(repo, id) + "#comment-" + strconv.Itoa(comment)
}
