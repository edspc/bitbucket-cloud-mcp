package tools

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

func (t *tools) stopPipeline(ctx context.Context, req *mcp.CallToolRequest, in pipelineRef) (*mcp.CallToolResult, statusResult, error) {
	page, err := t.claimSecrets(req, in)
	if err != nil {
		return nil, statusResult{}, err
	}
	defer page.close()
	ref := in
	if err = ref.validate(); err != nil {
		return nil, statusResult{}, err
	}
	p, err := t.bb.GetPipeline(ctx, ref.Repo, ref.Pipeline)
	if err != nil {
		return nil, statusResult{}, err
	}
	if uuid := strings.Trim(p.UUID, "{}"); len(uuid) != 36 || strings.Trim(uuid, "0123456789abcdefABCDEF-") != "" {
		return nil, statusResult{}, errors.New("bitbucket returned an invalid pipeline UUID; nothing changed")
	}
	details := struct {
		Repo        string `json:"repo"`
		Pipeline    string `json:"pipeline"`
		BuildNumber int    `json:"build_number"`
	}{in.Repo, p.UUID, p.BuildNumber}
	res, approved, err := t.confirmChange(ctx, req, in, page, details)
	if err != nil || res != nil {
		return res, statusResult{}, err
	}
	if !approved {
		return nil, statusResult{Status: "cancelled", Message: "the operation was not approved; no stop was requested"}, nil
	}
	if p.State.Completed() {
		return nil, statusResult{Status: "already_completed", Message: "the pipeline has already completed; no stop was requested"}, nil
	}
	if err = approvalLive(page); err != nil {
		return nil, statusResult{}, err
	}
	if err = t.bb.StopPipeline(bitbucket.WithWriteDeadline(ctx, page.page.info.Until), in.Repo, p.UUID); err != nil {
		return nil, statusResult{}, err
	}
	t.log.InfoContext(ctx, "requested pipeline stop", "repo", in.Repo, "build", p.BuildNumber)
	return nil, statusResult{Status: "stop_requested", Message: "stop signal sent; use get_pipeline or watch_pipeline to check the final state"}, nil
}
