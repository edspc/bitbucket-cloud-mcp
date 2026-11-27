package tools

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type manageRepositoryInput struct {
	Webhook           *webhookInput           `json:"webhook,omitempty" jsonschema:"manage repository webhooks in a separate call"`
	BranchRestriction *branchRestrictionInput `json:"branch_restriction,omitempty" jsonschema:"manage one Git branch rule separately from repository settings"`
	Repo              string                  `json:"repo" jsonschema:"current repository slug"`
	Slug              string                  `json:"slug,omitempty" jsonschema:"new lowercase slug; also changes the repository name"`
	Project           string                  `json:"project,omitempty" jsonschema:"destination project key in the configured workspace"`
	Description       *string                 `json:"description,omitempty" jsonschema:"new description; use an empty string to clear it"`
	Language          *string                 `json:"language,omitempty" jsonschema:"Bitbucket language identifier; use an empty string to clear it"`
}

type manageRepositoryResult struct {
	Webhook                    *bitbucket.Webhook      `json:"webhook,omitempty"`
	Webhooks                   *[]bitbucket.Webhook    `json:"webhooks,omitempty"`
	DeletedWebhookUUID         string                  `json:"deleted_webhook_uuid,omitempty"`
	ContentTrust               string                  `json:"content_trust"`
	Repository                 *repositoryView         `json:"repository,omitempty"`
	BranchRestriction          *bitbucket.BranchRule   `json:"branch_restriction,omitempty"`
	BranchRestrictions         *[]bitbucket.BranchRule `json:"branch_restrictions,omitempty"`
	DeletedBranchRestrictionID int                     `json:"deleted_branch_restriction_id,omitempty"`
	NextPage                   int                     `json:"next_page,omitempty"`
	Total                      *int                    `json:"total,omitempty"`
}

type getRepositoryInput struct {
	Webhook *webhookReadInput `json:"webhook,omitempty" jsonschema:"list repository webhooks or read one by uuid"`
	Repo    string            `json:"repo" jsonschema:"repository slug"`
}

func (t *tools) getRepository(ctx context.Context, _ *mcp.CallToolRequest, in getRepositoryInput) (*mcp.CallToolResult, manageRepositoryResult, error) {
	if err := t.policy.checkRepo(in.Repo); err != nil {
		return nil, manageRepositoryResult{}, err
	}
	if in.Webhook != nil {
		out, err := t.readWebhooks(ctx, in.Repo, *in.Webhook)
		return nil, out, err
	}
	repo, err := t.bb.RepositorySettings(ctx, in.Repo)
	if err != nil {
		return nil, manageRepositoryResult{}, err
	}
	view := t.repositoryView(repo)
	return nil, manageRepositoryResult{ContentTrust: "untrusted", Repository: &view}, nil
}

func (in manageRepositoryInput) hasSettings() bool {
	return in.Slug != "" || in.Project != "" || in.Description != nil || in.Language != nil
}

func (t *tools) manageRepository(ctx context.Context, req *mcp.CallToolRequest, in manageRepositoryInput) (*mcp.CallToolResult, manageRepositoryResult, error) {
	if err := t.policy.checkRepo(in.Repo); err != nil {
		return nil, manageRepositoryResult{}, err
	}
	hasSettings := in.hasSettings()
	if in.Webhook != nil {
		if hasSettings || in.BranchRestriction != nil {
			return nil, manageRepositoryResult{}, errors.New("webhook must be called separately from repository settings and branch restrictions")
		}
		return t.manageWebhook(ctx, req, in)
	}
	if in.BranchRestriction != nil {
		if hasSettings {
			return nil, manageRepositoryResult{}, errors.New("branch_restriction must be called separately from repository settings changes")
		}
		return t.manageBranchRestriction(ctx, req, in)
	}
	if !hasSettings {
		return t.getRepository(ctx, req, getRepositoryInput{Repo: in.Repo})
	}
	page, err := t.claimSecrets(req, in)
	if err != nil {
		return nil, manageRepositoryResult{}, err
	}
	defer page.close()
	if in.Slug != "" && !validNewSlug(in.Slug) {
		return nil, manageRepositoryResult{}, errors.New("slug must be a lowercase Bitbucket Cloud slug of at most 62 characters")
	}
	if strings.Trim(in.Project, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return nil, manageRepositoryResult{}, errors.New("project must be a project key in the configured workspace")
	}
	before, err := t.bb.RepositorySettings(ctx, in.Repo)
	if err != nil {
		return nil, manageRepositoryResult{}, err
	}
	slug, key, projectUUID := before.Slug, before.Project.Key, before.Project.UUID
	if in.Slug != "" {
		if !bitbucket.ValidRepositorySlug(slug) {
			return nil, manageRepositoryResult{}, errors.New("bitbucket returned no usable source repository slug")
		}
		if err = t.policy.checkRenameToolRules(slug, in.Slug); err != nil {
			return nil, manageRepositoryResult{}, err
		}
		if !strings.EqualFold(in.Repo, slug) {
			if err = t.policy.checkRenameToolRules(in.Repo, in.Slug); err != nil {
				return nil, manageRepositoryResult{}, err
			}
		}
		slug = in.Slug
	}
	update := bitbucket.RepositoryUpdate{Name: in.Slug, Description: in.Description, Language: in.Language}
	if in.Project != "" && in.Project != key {
		key, projectUUID = in.Project, ""
		if !t.policy.allowsListed(slug, key, "") &&
			slices.ContainsFunc(t.policy.Projects, func(allowed string) bool { return strings.HasPrefix(allowed, "{") }) {
			project, err := t.bb.RepositoryProject(ctx, key)
			if err != nil {
				return nil, manageRepositoryResult{}, err
			}
			if project.Key != key || project.UUID == "" {
				return nil, manageRepositoryResult{}, errors.New("bitbucket returned no matching destination project identity")
			}
			projectUUID = project.UUID
		}
		update.Project = &bitbucket.RepositoryProject{Key: key}
	} else if in.Project != "" {
		update.Project = &bitbucket.RepositoryProject{Key: key}
	}
	if !t.policy.allowsListed(slug, key, projectUUID) {
		return nil, manageRepositoryResult{}, errors.New("destination repository or project is not allowed by the server policy")
	}
	details := struct {
		manageRepositoryInput
		RepositoryUUID string `json:"repository_uuid"`
		CurrentProject string `json:"current_project"`
		ProjectUUID    string `json:"destination_project_uuid,omitempty"`
	}{in, before.UUID, before.Project.Key, projectUUID}
	res, approved, err := t.confirmChange(ctx, req, in, page, details)
	if err != nil || res != nil {
		return res, manageRepositoryResult{}, err
	}
	if !approved {
		return nil, manageRepositoryResult{}, errors.New("repository update was not approved")
	}
	if err = approvalLive(page); err != nil {
		return nil, manageRepositoryResult{}, err
	}
	updated, err := t.bb.UpdateRepository(bitbucket.WithWriteDeadline(ctx, page.page.info.Until), in.Repo, before, update, projectUUID)
	if err != nil {
		return nil, manageRepositoryResult{}, err
	}
	view := t.repositoryView(updated)
	return nil, manageRepositoryResult{ContentTrust: "untrusted", Repository: &view}, nil
}

func validNewSlug(slug string) bool {
	if len(slug) > 62 || slug == "." || slug == ".." || strings.Trim(slug, "abcdefghijklmnopqrstuvwxyz0123456789._-") != "" {
		return false
	}
	return strings.Trim(slug, "-") == "" ||
		!strings.HasPrefix(slug, "-") && !strings.HasSuffix(slug, "-") && !strings.Contains(slug, "--")
}
