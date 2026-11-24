package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type manageDeploymentInput struct {
	Repo            string   `json:"repo" jsonschema:"repository slug"`
	Name            string   `json:"name" jsonschema:"deployment environment name"`
	Create          bool     `json:"create,omitempty" jsonschema:"create a new environment; omit or set to false to find an existing environment"`
	EnvironmentType string   `json:"environment_type,omitempty" jsonschema:"required for creation: Test, Staging or Production"`
	AllowedBranches []string `json:"allowed_branches,omitempty" jsonschema:"unsupported by the Bitbucket Cloud public API"`
	AllowedUsers    []string `json:"allowed_users,omitempty" jsonschema:"unsupported by the Bitbucket Cloud public API"`
	AllowedGroups   []string `json:"allowed_groups,omitempty" jsonschema:"unsupported by the Bitbucket Cloud public API"`
}

type manageDeploymentResult struct {
	Status       string                 `json:"status"`
	ContentTrust string                 `json:"content_trust"`
	Environment  *bitbucket.Environment `json:"environment,omitempty"`
}

func (t *tools) manageDeployment(ctx context.Context, req *mcp.CallToolRequest, in manageDeploymentInput) (*mcp.CallToolResult, manageDeploymentResult, error) {
	if err := t.policy.checkRepo(in.Repo); err != nil {
		return nil, manageDeploymentResult{}, err
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Name) != in.Name {
		return nil, manageDeploymentResult{}, errors.New("a nonempty name without surrounding whitespace is required")
	}
	if in.AllowedBranches != nil || in.AllowedUsers != nil || in.AllowedGroups != nil {
		return nil, manageDeploymentResult{}, errors.New("deployment restrictions for branches, users or groups are unavailable through the Bitbucket Cloud public API; configure them under Repository settings > Pipelines > Deployments")
	}
	if !in.Create {
		if in.EnvironmentType != "" {
			return nil, manageDeploymentResult{}, errors.New("environment_type requires create: true")
		}
		envs, err := t.bb.ListEnvironments(ctx, in.Repo)
		if err != nil {
			return nil, manageDeploymentResult{}, err
		}
		for _, env := range envs {
			if env.Name == in.Name {
				return nil, manageDeploymentResult{Status: "existing", ContentTrust: "untrusted", Environment: &env}, nil
			}
		}
		return nil, manageDeploymentResult{}, fmt.Errorf("deployment environment %q not found; pass create: true to create it", in.Name)
	}
	page, err := t.claimSecrets(req, in)
	if err != nil {
		return nil, manageDeploymentResult{}, err
	}
	defer page.close()
	switch in.EnvironmentType {
	case "Test", "Staging", "Production":
	default:
		return nil, manageDeploymentResult{}, errors.New("environment_type must be Test, Staging or Production")
	}
	if err = t.checkPipelines(ctx, in.Repo); err != nil {
		return nil, manageDeploymentResult{}, err
	}
	res, approved, err := t.confirmChange(ctx, req, in, page, in)
	if err != nil || res != nil {
		return res, manageDeploymentResult{}, err
	}
	if !approved {
		return nil, manageDeploymentResult{Status: "cancelled"}, nil
	}
	// Keep the duplicate check and creation together, after the browser wait.
	t.scheduleMu.Lock()
	defer t.scheduleMu.Unlock()
	checkCtx, cancel := context.WithDeadline(ctx, page.page.info.Until)
	defer cancel()
	envs, err := t.bb.ListEnvironments(checkCtx, in.Repo)
	if err != nil {
		return nil, manageDeploymentResult{}, err
	}
	for _, e := range envs {
		if strings.EqualFold(e.Name, in.Name) {
			return nil, manageDeploymentResult{}, fmt.Errorf("deployment environment %q already exists; nothing changed", in.Name)
		}
	}
	if err = approvalLive(page); err != nil {
		return nil, manageDeploymentResult{}, err
	}
	env, err := t.bb.CreateEnvironment(bitbucket.WithWriteDeadline(ctx, page.page.info.Until), in.Repo, in.Name, in.EnvironmentType)
	if err != nil {
		return nil, manageDeploymentResult{}, err
	}
	t.log.InfoContext(ctx, "created deployment environment", "repo", in.Repo, "environment", env.UUID)
	return nil, manageDeploymentResult{Status: "created", ContentTrust: "untrusted", Environment: &env}, nil
}
