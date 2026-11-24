package bitbucket

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type BranchRuleUser struct {
	UUID      string `json:"uuid,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

type BranchRuleGroup struct {
	Slug string `json:"slug"`
}

type BranchRuleFields struct {
	Kind                    string             `json:"kind,omitempty"`
	BranchMatchKind         string             `json:"branch_match_kind,omitempty"`
	BranchType              string             `json:"branch_type,omitempty"`
	Pattern                 *string            `json:"pattern,omitempty"`
	Value                   *int               `json:"value,omitempty"`
	Users                   *[]BranchRuleUser  `json:"users,omitempty"`
	Groups                  *[]BranchRuleGroup `json:"groups,omitempty"`
	PipelinesSourceBranches *[]string          `json:"pipelines_source_branches,omitempty"`
}

type BranchRule struct {
	ID int `json:"id"`
	BranchRuleFields
}

func (c *Client) ListBranchRules(ctx context.Context, repo string, page, limit int) (Page[BranchRule], error) {
	query := url.Values{"page": {strconv.Itoa(page)}, "pagelen": {strconv.Itoa(limit)}}
	result, err := readPage[BranchRule](ctx, c, APIURL+c.repoPath(repo, "branch-restrictions")+"?"+query.Encode(), limit)
	return Page[BranchRule]{Values: result.Values, More: result.Next != "", Total: result.Size}, err
}

func (c *Client) BranchRule(ctx context.Context, repo string, id int) (BranchRule, error) {
	var result BranchRule
	err := c.call(ctx, http.MethodGet, c.repoPath(repo, "branch-restrictions", strconv.Itoa(id)), nil, &result)
	return result, err
}

func (c *Client) CreateBranchRule(ctx context.Context, repo string, before Repository, fields BranchRuleFields) (BranchRule, error) {
	return c.callPinnedBranchRule(ctx, http.MethodPost, repo, before, 0, fields)
}

func (c *Client) UpdateBranchRule(ctx context.Context, repo string, before Repository, id int, fields BranchRuleFields) (BranchRule, error) {
	return c.callPinnedBranchRule(ctx, http.MethodPut, repo, before, id, fields)
}

func (c *Client) DeleteBranchRule(ctx context.Context, repo string, before Repository, id int) error {
	_, err := c.callPinnedBranchRule(ctx, http.MethodDelete, repo, before, id, nil)
	return err
}

func (c *Client) PinnedBranchRule(ctx context.Context, repo string, before Repository, id int) (BranchRule, error) {
	return c.callPinnedBranchRule(ctx, http.MethodGet, repo, before, id, nil)
}

func (c *Client) callPinnedBranchRule(ctx context.Context, method, repo string, before Repository, id int, body any) (BranchRule, error) {
	checkCtx, cancel := repositoryCheckContext(ctx)
	defer cancel()
	if err := c.checkRepositoryIdentity(checkCtx, repo, before); err != nil {
		return BranchRule{}, err
	}
	// Keep the API target on the authorized repository even if the slug is reused.
	parts := []string{"branch-restrictions"}
	if id != 0 {
		parts = append(parts, strconv.Itoa(id))
	}
	var rule BranchRule
	var out any = &rule
	if method == http.MethodDelete {
		out = nil
	}
	err := c.sendUnchecked(ctx, method, APIURL+c.repoPath(before.UUID, parts...), c.basicAuth, body, out)
	return rule, err
}
