package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

type listCommitsInput struct {
	Repo          string `json:"repo" jsonschema:"repository slug"`
	Revision      string `json:"revision,omitempty" jsonschema:"branch, tag or commit; omitted includes all branches and tags; use the returned revision SHA for later pages"`
	Path          string `json:"path,omitempty" jsonschema:"file or directory path; mutually exclusive with prefix; omitted lists repository history"`
	Prefix        string `json:"prefix,omitempty" jsonschema:"literal path prefix without wildcard characters; mutually exclusive with path"`
	FollowRenames bool   `json:"follow_renames,omitempty" jsonschema:"follow a single file across renames; requires path and revision; unavailable with prefix"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque next_cursor from the previous result; repeat the same filters, limit and returned revision SHA"`
	Limit         int    `json:"limit,omitempty" jsonschema:"commits per page; 10 when omitted, at most 50"`
}

type listCommitsResult struct {
	ContentTrust string                   `json:"content_trust"`
	Revision     string                   `json:"revision,omitempty"`
	Commits      []bitbucket.HistoryEntry `json:"commits"`
	NextCursor   string                   `json:"next_cursor,omitempty"`
	Total        *int                     `json:"total,omitempty"`
}

func (t *tools) listCommits(ctx context.Context, _ *mcp.CallToolRequest, in listCommitsInput) (*mcp.CallToolResult, listCommitsResult, error) {
	if in.Path != "" && in.Prefix != "" {
		return nil, listCommitsResult{}, errors.New("supply either path or prefix, not both")
	}
	if in.FollowRenames && (in.Path == "" || in.Revision == "" || in.Prefix != "") {
		return nil, listCommitsResult{}, errors.New("follow_renames requires a file path and revision, without prefix")
	}
	if strings.ContainsAny(in.Prefix, "*?[]:") || strings.ContainsAny(in.Path, "*?[]:") {
		return nil, listCommitsResult{}, errors.New("path and prefix must be literal paths without wildcard or pathspec characters")
	}
	path := in.Path
	if in.Prefix != "" {
		path = in.Prefix
	}
	if strings.HasPrefix(path, "/") || in.FollowRenames && strings.HasSuffix(path, "/") {
		return nil, listCommitsResult{}, errors.New("path must be repository-relative; follow_renames requires a file path")
	}
	validation := sourceInput{Repo: in.Repo, Revision: in.Revision, Path: strings.TrimSuffix(path, "/")}
	if validation.Revision == "" {
		validation.Revision = "HEAD"
	}
	if err := validation.validate(true, in.Revision != "" && in.Cursor != ""); err != nil {
		return nil, listCommitsResult{}, err
	}
	limit, err := optionalCount("limit", in.Limit, defaultListLimit, maxListLimit)
	if err != nil {
		return nil, listCommitsResult{}, err
	}
	revision := in.Revision
	if revision != "" {
		revision, err = t.bb.ResolveRevision(ctx, in.Repo, revision)
		if err != nil {
			return nil, listCommitsResult{}, err
		}
	}
	if in.Prefix != "" {
		path += "*"
	}
	commits, err := t.bb.ListCommits(ctx, in.Repo, revision, path, in.FollowRenames, in.Cursor, limit)
	if err != nil {
		return nil, listCommitsResult{}, err
	}
	values := commits.Values
	if values == nil {
		values = []bitbucket.HistoryEntry{}
	}
	result := listCommitsResult{ContentTrust: "untrusted", Revision: revision, Commits: values, NextCursor: commits.NextCursor, Total: commits.Total}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, listCommitsResult{}, err
	}
	if len(data) > sourceChunkBytes {
		return nil, listCommitsResult{}, errors.New("history page exceeds 40 KiB; reduce limit and restart without cursor")
	}
	return nil, result, nil
}
