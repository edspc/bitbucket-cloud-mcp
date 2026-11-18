package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createPullRequestCommentInput struct {
	Repo        string `json:"repo" jsonschema:"repository slug"`
	PullRequest int    `json:"pull_request" jsonschema:"positive pull request ID"`
	Content     string `json:"content" jsonschema:"nonblank raw Markdown, at most 32 KiB; preserved exactly and shown for browser confirmation"`
	ParentID    int    `json:"parent_id,omitempty" jsonschema:"comment ID in this PR to reply to, from list_pull_request_comments; omit or use 0 for a new general comment"`
}

type createPullRequestCommentResult struct {
	Status    string `json:"status"`
	CommentID int    `json:"comment_id,omitempty"`
	ParentID  int    `json:"parent_id,omitempty"`
	URL       string `json:"url,omitempty"`
}

// commentTarget verifies the PR and binds approval to the current parent comment.
func (t *tools) commentTarget(ctx context.Context, in createPullRequestCommentInput) (string, error) {
	pr, err := t.bb.GetPullRequest(ctx, in.Repo, in.PullRequest)
	if err != nil {
		return "", err
	}
	if pr.ID != in.PullRequest {
		return "", errors.New("bitbucket returned a different pull request; nothing changed")
	}
	if in.ParentID == 0 {
		return "", nil
	}
	parent, err := t.bb.GetPullRequestComment(ctx, in.Repo, in.PullRequest, in.ParentID)
	if err != nil {
		return "", err
	}
	if parent.ID != in.ParentID || parent.Deleted || parent.Pending {
		return "", errors.New("parent comment is missing, deleted or pending; nothing changed")
	}
	data, err := json.Marshal(parent)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (t *tools) createPullRequestComment(ctx context.Context, req *mcp.CallToolRequest, in createPullRequestCommentInput) (*mcp.CallToolResult, createPullRequestCommentResult, error) {
	page, err := t.claimSecrets(req, in)
	if err != nil {
		return nil, createPullRequestCommentResult{}, err
	}
	defer page.close()
	if in.Repo == "" || in.PullRequest < 1 || in.ParentID < 0 || strings.TrimSpace(in.Content) == "" || len(in.Content) > bitbucket.MaxCommentBytes {
		return nil, createPullRequestCommentResult{}, errors.New("repo, a positive pull_request, nonnegative parent_id and nonblank content of at most 32 KiB are required")
	}
	version, err := t.commentTarget(ctx, in)
	if err != nil {
		return nil, createPullRequestCommentResult{}, err
	}
	details := struct {
		createPullRequestCommentInput
		PullRequestURL string `json:"pull_request_url"`
		ParentURL      string `json:"parent_url,omitempty"`
		ParentVersion  string `json:"parent_version,omitempty"`
	}{createPullRequestCommentInput: in, PullRequestURL: t.bb.PullRequestURL(in.Repo, in.PullRequest), ParentVersion: version}
	if in.ParentID > 0 {
		details.ParentURL = t.bb.PullRequestCommentURL(in.Repo, in.PullRequest, in.ParentID)
	}
	res, approved, err := t.confirmChange(ctx, req, in, page, details)
	if err != nil || res != nil {
		return res, createPullRequestCommentResult{}, err
	}
	if !approved {
		return nil, createPullRequestCommentResult{Status: "cancelled"}, nil
	}
	checkCtx, cancel := context.WithDeadline(ctx, page.page.info.Until)
	defer cancel()
	current, err := t.commentTarget(checkCtx, in)
	if err != nil {
		return nil, createPullRequestCommentResult{}, err
	}
	if current != version {
		return nil, createPullRequestCommentResult{}, errors.New("parent comment changed during approval; nothing changed; read the thread and request a fresh confirmation")
	}
	if err = approvalLive(page); err != nil {
		return nil, createPullRequestCommentResult{}, err
	}
	comment, err := t.bb.CreatePullRequestComment(bitbucket.WithWriteDeadline(ctx, page.page.info.Until), in.Repo, in.PullRequest, in.Content, in.ParentID)
	if err != nil {
		return nil, createPullRequestCommentResult{}, fmt.Errorf("comment creation failed; inspect the PR before retrying to avoid a duplicate: %w", err)
	}
	if comment.ID < 1 {
		return nil, createPullRequestCommentResult{}, errors.New("bitbucket returned no valid comment ID; the comment may have been created; inspect the PR before retrying")
	}
	t.log.InfoContext(ctx, "created pull request comment", "repo", in.Repo, "pull_request", in.PullRequest, "comment_id", comment.ID, "parent_id", in.ParentID)
	return nil, createPullRequestCommentResult{Status: "created", CommentID: comment.ID, ParentID: in.ParentID, URL: t.bb.PullRequestCommentURL(in.Repo, in.PullRequest, comment.ID)}, nil
}
