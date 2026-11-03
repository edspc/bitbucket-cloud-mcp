package tools

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

type apiRequestInput struct {
	bitbucket.APIRequest
}

type apiRequestResult struct {
	ContentTrust string `json:"content_trust"`
	bitbucket.APIResponse
}

func (t *tools) apiRequest(ctx context.Context, req *mcp.CallToolRequest, in apiRequestInput) (*mcp.CallToolResult, apiRequestResult, error) {
	if !t.policy.APIProxyEnabled {
		return nil, apiRequestResult{}, errors.New("api_request is disabled; the operator must set BITBUCKET_ENABLE_API_PROXY=true")
	}
	// Opaque paths and bodies can name more entities than their URL's repository. Fail closed
	// rather than pretending the dedicated tools' repository and ref checks cover arbitrary APIs.
	if t.policy.Repositories != nil || t.policy.Projects != nil || t.policy.Refs != nil {
		return nil, apiRequestResult{}, errors.New("api_request is unavailable while repository, project or ref limits are configured; use the dedicated policy-aware tools")
	}
	page, err := t.claimSecrets(req, in)
	if err != nil {
		return nil, apiRequestResult{}, err
	}
	defer page.close()
	if err = t.bb.ValidateAPIRequest(in.APIRequest); err != nil {
		return nil, apiRequestResult{}, err
	}
	res, approved, err := t.confirmChange(ctx, req, in, page, in)
	if err != nil || res != nil {
		return res, apiRequestResult{}, err
	}
	if !approved {
		return nil, apiRequestResult{}, errors.New("the API request was not approved; nothing changed")
	}
	if err = approvalLive(page); err != nil {
		return nil, apiRequestResult{}, err
	}
	out, err := t.bb.DoAPIRequest(bitbucket.WithWriteDeadline(ctx, page.page.info.Until), in.APIRequest)
	if err != nil {
		return nil, apiRequestResult{}, err
	}
	return nil, apiRequestResult{ContentTrust: "untrusted", APIResponse: out}, nil
}
