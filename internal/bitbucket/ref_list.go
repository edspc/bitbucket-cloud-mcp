package bitbucket

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

type Ref struct {
	Name    string `json:"name"`
	Target  Commit `json:"target"`
	Message string `json:"message,omitempty"`
}

func (c *Client) ListRefs(ctx context.Context, repo, kind string, page, limit int) (Page[Ref], error) {
	if kind != "branches" && kind != "tags" {
		return Page[Ref]{}, fmt.Errorf("unsupported ref collection %q", kind)
	}
	q := url.Values{"sort": {"name"}, "page": {strconv.Itoa(page)}, "pagelen": {strconv.Itoa(limit)}}
	p, err := readPage[Ref](ctx, c, APIURL+c.repoPath(repo, "refs", kind)+"?"+q.Encode(), limit)
	return Page[Ref]{Values: p.Values, Total: p.Size, More: p.Next != ""}, err
}
