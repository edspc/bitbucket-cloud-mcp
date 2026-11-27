package tools

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type repositoryAction struct {
	Webhook           *webhookInput           `json:"webhook,omitempty" jsonschema:"one webhook operation"`
	BranchRestriction *branchRestrictionInput `json:"branch_restriction,omitempty" jsonschema:"one Git branch restriction operation"`
	Slug              string                  `json:"slug,omitempty" jsonschema:"new lowercase slug; also changes the repository name"`
	Project           string                  `json:"project,omitempty" jsonschema:"destination project key in the configured workspace"`
	Description       *string                 `json:"description,omitempty" jsonschema:"new description; use an empty string to clear it"`
	Language          *string                 `json:"language,omitempty" jsonschema:"Bitbucket language identifier; use an empty string to clear it"`
}

type manageRepositoryInput struct {
	repositoryAction
	Repo    string             `json:"repo,omitempty" jsonschema:"one current repository slug; use repo or repos"`
	Repos   []string           `json:"repos,omitempty" jsonschema:"distinct current repository slugs in execution order; use repo or repos"`
	Actions []repositoryAction `json:"actions,omitempty" jsonschema:"ordered actions applied to each selected repository; at most 100 repository/action combinations; omit to use the top-level action"`
}

type repositoryActionResult struct {
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

type manageRepositoryResult struct {
	repositoryActionResult
	Status  string                 `json:"status,omitempty"`
	Results []repositoryBatchEntry `json:"results,omitempty"`
}

type getRepositoryInput struct {
	Webhook *webhookReadInput `json:"webhook,omitempty" jsonschema:"list repository webhooks or read one by uuid"`
	Repo    string            `json:"repo" jsonschema:"repository slug"`
}

func (t *tools) getRepository(ctx context.Context, _ *mcp.CallToolRequest, in getRepositoryInput) (*mcp.CallToolResult, repositoryActionResult, error) {
	if err := t.policy.checkRepo(in.Repo); err != nil {
		return nil, repositoryActionResult{}, err
	}
	if in.Webhook != nil {
		out, err := t.readWebhooks(ctx, in.Repo, *in.Webhook, nil)
		return nil, out, err
	}
	out, err := t.readRepositorySettings(ctx, in.Repo, nil)
	return nil, out, err
}

func (t *tools) readRepositorySettings(ctx context.Context, repo string, before *bitbucket.Repository) (repositoryActionResult, error) {
	var settings bitbucket.Repository
	var err error
	if before == nil {
		settings, err = t.bb.RepositorySettings(ctx, repo)
	} else {
		settings, err = t.bb.CheckedRepositorySettings(ctx, repo, *before)
	}
	if err != nil {
		return repositoryActionResult{}, err
	}
	view := t.repositoryView(settings)
	return repositoryActionResult{ContentTrust: "untrusted", Repository: &view}, nil
}

func repositoryListPage(page, limit int) (int, int, error) {
	page, err := optionalPage(page)
	if err != nil {
		return 0, 0, err
	}
	limit, err = optionalCount("limit", limit, defaultListLimit, maxListLimit)
	return page, limit, err
}

func (a repositoryAction) hasSettings() bool {
	return a.Slug != "" || a.Project != "" || a.Description != nil || a.Language != nil
}

func (a repositoryAction) writes() bool {
	return a.hasSettings() ||
		a.Webhook != nil && a.Webhook.Action != "list" && a.Webhook.Action != "get" ||
		a.BranchRestriction != nil && a.BranchRestriction.Action != "list" && a.BranchRestriction.Action != "get"
}

func (a repositoryAction) validate() error {
	switch {
	case a.Webhook != nil:
		if a.hasSettings() || a.BranchRestriction != nil {
			return errors.New("use a separate action for a webhook, branch restriction or repository settings")
		}
		return a.Webhook.validate()
	case a.BranchRestriction != nil:
		if a.hasSettings() {
			return errors.New("use a separate action for a branch restriction and repository settings")
		}
		return a.BranchRestriction.validate()
	}
	if a.Slug != "" && !validNewSlug(a.Slug) {
		return errors.New("slug must be a lowercase Bitbucket Cloud slug of at most 62 characters")
	}
	if strings.Trim(a.Project, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return errors.New("project must be a project key in the configured workspace")
	}
	return nil
}

func (t *tools) readRepositoryAction(ctx context.Context, repo string, action repositoryAction, before *bitbucket.Repository) (repositoryActionResult, error) {
	switch {
	case action.Webhook != nil:
		return t.readWebhooks(ctx, repo, action.Webhook.webhookReadInput, before)
	case action.BranchRestriction != nil:
		return t.readBranchRestriction(ctx, repo, *action.BranchRestriction, before)
	default:
		return t.readRepositorySettings(ctx, repo, before)
	}
}

func (t *tools) manageRepository(ctx context.Context, req *mcp.CallToolRequest, in manageRepositoryInput) (*mcp.CallToolResult, manageRepositoryResult, error) {
	if in.batch() || in.writes() {
		return t.manageRepositoryOperations(ctx, req, in)
	}
	if err := t.policy.checkRepo(in.Repo); err != nil {
		return nil, manageRepositoryResult{}, err
	}
	if err := in.validate(); err != nil {
		return nil, manageRepositoryResult{}, err
	}
	out, err := t.readRepositoryAction(ctx, in.Repo, in.repositoryAction, nil)
	return nil, manageRepositoryResult{repositoryActionResult: out}, err
}

func (t *tools) planRepositorySettings(ctx context.Context, repo string, before bitbucket.Repository, action repositoryAction) (repositoryOperation, error) {
	slug, key, projectUUID := before.Slug, before.Project.Key, before.Project.UUID
	if action.Slug != "" {
		if !bitbucket.ValidRepositorySlug(slug) {
			return repositoryOperation{}, errors.New("bitbucket returned no usable source repository slug")
		}
		if err := t.policy.checkRenameToolRules(slug, action.Slug); err != nil {
			return repositoryOperation{}, err
		}
		if !strings.EqualFold(repo, slug) {
			if err := t.policy.checkRenameToolRules(repo, action.Slug); err != nil {
				return repositoryOperation{}, err
			}
		}
		slug = action.Slug
	}
	update := bitbucket.RepositoryUpdate{Name: action.Slug, Description: action.Description, Language: action.Language}
	if action.Project != "" {
		if action.Project != key {
			key, projectUUID = action.Project, ""
			if !t.policy.allowsListed(slug, key, "") &&
				slices.ContainsFunc(t.policy.Projects, func(allowed string) bool { return strings.HasPrefix(allowed, "{") }) {
				project, err := t.bb.RepositoryProject(ctx, key)
				if err != nil {
					return repositoryOperation{}, err
				}
				if project.Key != key || project.UUID == "" {
					return repositoryOperation{}, errors.New("bitbucket returned no matching destination project identity")
				}
				projectUUID = project.UUID
			}
		}
		update.Project = &bitbucket.RepositoryProject{Key: key}
	}
	if !t.policy.allowsListed(slug, key, projectUUID) {
		return repositoryOperation{}, errors.New("destination repository or project is not allowed by the server policy")
	}
	return repositoryOperation{
		approval: repositoryActionApproval{DestinationProjectUUID: projectUUID},
		apply: func(ctx context.Context, until time.Time, _ string) (repositoryActionResult, error) {
			updated, err := t.bb.UpdateRepository(bitbucket.WithWriteDeadline(ctx, until), repo, before, update, projectUUID)
			if err != nil {
				return repositoryActionResult{}, err
			}
			view := t.repositoryView(updated)
			return repositoryActionResult{ContentTrust: "untrusted", Repository: &view}, nil
		},
	}, nil
}

func validNewSlug(slug string) bool {
	if len(slug) > 62 || slug == "." || slug == ".." || strings.Trim(slug, "abcdefghijklmnopqrstuvwxyz0123456789._-") != "" {
		return false
	}
	return strings.Trim(slug, "-") == "" ||
		!strings.HasPrefix(slug, "-") && !strings.HasSuffix(slug, "-") && !strings.Contains(slug, "--")
}
