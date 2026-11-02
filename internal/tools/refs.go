package tools

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

type listRefsInput struct {
	Repo  string `json:"repo" jsonschema:"repository slug"`
	Limit int    `json:"limit,omitempty" jsonschema:"refs per page; 10 when omitted, at most 50"`
	Page  int    `json:"page,omitempty" jsonschema:"page from 1; pass next_page with the same limit for more"`
}

type listRefsResult struct {
	ContentTrust string          `json:"content_trust"`
	Refs         []bitbucket.Ref `json:"refs"`
	NextPage     int             `json:"next_page,omitempty"`
	Total        *int            `json:"total,omitempty"`
}

func (t *tools) listBranches(ctx context.Context, _ *mcp.CallToolRequest, in listRefsInput) (*mcp.CallToolResult, listRefsResult, error) {
	result, err := t.listRefs(ctx, in, "branches")
	return nil, result, err
}

func (t *tools) listTags(ctx context.Context, _ *mcp.CallToolRequest, in listRefsInput) (*mcp.CallToolResult, listRefsResult, error) {
	result, err := t.listRefs(ctx, in, "tags")
	return nil, result, err
}

func (t *tools) listRefs(ctx context.Context, in listRefsInput, kind string) (listRefsResult, error) {
	if in.Repo == "" {
		return listRefsResult{}, errors.New("repo is required")
	}
	limit, err := optionalCount("limit", in.Limit, defaultListLimit, maxListLimit)
	if err != nil {
		return listRefsResult{}, err
	}
	page, err := optionalPage(in.Page)
	if err != nil {
		return listRefsResult{}, err
	}
	refs, err := t.bb.ListRefs(ctx, in.Repo, kind, page, limit)
	if err != nil {
		return listRefsResult{}, err
	}
	values := refs.Values
	if values == nil {
		values = []bitbucket.Ref{}
	}
	return listRefsResult{ContentTrust: "untrusted", Refs: values, NextPage: nextPage(page, refs.More), Total: refs.Total}, nil
}
