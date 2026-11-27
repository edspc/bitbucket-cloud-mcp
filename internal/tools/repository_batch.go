package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxRepositoryOperations    = 100
	maxRepositoryApprovalBytes = 512 << 10
	maxRepositoryResultBytes   = 256 << 10
	maxWebhookSecretRunes      = 128
)

type repositoryBatchEntry struct {
	Repo          string                  `json:"repo"`
	Action        int                     `json:"action" jsonschema:"one-based index in actions, or 1 for a top-level action"`
	Status        string                  `json:"status" jsonschema:"completed, failed or skipped"`
	Result        *repositoryActionResult `json:"result,omitempty"`
	ResultOmitted bool                    `json:"result_omitted,omitempty"`
	Error         string                  `json:"error,omitempty"`
}

type repositoryActionApproval struct {
	repositoryAction
	CurrentRule            *bitbucket.BranchRule `json:"current_rule,omitempty"`
	CurrentWebhook         *bitbucket.Webhook    `json:"current_webhook,omitempty"`
	DestinationProjectUUID string                `json:"destination_project_uuid,omitempty"`
	SigningSecretField     string                `json:"signing_secret_field,omitempty"`
}

type repositoryTargetApproval struct {
	Repo        string                     `json:"repo"`
	UUID        string                     `json:"repository_uuid"`
	Project     string                     `json:"current_project,omitempty"`
	ProjectUUID string                     `json:"current_project_uuid,omitempty"`
	Actions     []repositoryActionApproval `json:"actions"`
}

type repositoryOperation struct {
	approval  repositoryActionApproval
	hiddenURL string
	apply     func(context.Context, time.Time, string) (repositoryActionResult, error)
}

type repositoryPlan struct {
	targets     []repositoryTargetApproval
	operations  []repositoryOperation
	webhookURLs []string
}

func (in manageRepositoryInput) batch() bool {
	return in.Repos != nil || in.Actions != nil
}

func (in manageRepositoryInput) selectedActions() []repositoryAction {
	if in.Actions != nil {
		return in.Actions
	}
	return []repositoryAction{in.repositoryAction}
}

func (in manageRepositoryInput) operationInputs() ([]string, []repositoryAction, error) {
	repos := in.Repos
	if repos == nil && in.Repo != "" {
		repos = []string{in.Repo}
	}
	if len(repos) == 0 || in.Repo != "" && in.Repos != nil {
		return nil, nil, errors.New("provide either repo or a nonempty repos list")
	}
	actions := in.selectedActions()
	if len(actions) == 0 || in.Actions != nil && in.repositoryAction != (repositoryAction{}) {
		return nil, nil, errors.New("provide either top-level operation fields or a nonempty actions list")
	}
	if len(repos) > maxRepositoryOperations/len(actions) {
		return nil, nil, fmt.Errorf("at most %d repository/action combinations are allowed", maxRepositoryOperations)
	}
	seen := make(map[string]bool)
	for _, repo := range repos {
		key := strings.ToLower(repo)
		if seen[key] {
			return nil, nil, fmt.Errorf("duplicate repository %q", repo)
		}
		seen[key] = true
	}
	rules := make(map[int]bool)
	hooks := make(map[string]bool)
	for i, action := range actions {
		if err := action.validate(); err != nil {
			return nil, nil, fmt.Errorf("action %d: %w", i+1, err)
		}
		if action.Slug != "" && len(repos) != 1 {
			return nil, nil, errors.New("renaming requires exactly one repository")
		}
		// Later actions would need to use a different slug or a new project authorization.
		if (action.Slug != "" || action.Project != "") && i != len(actions)-1 {
			return nil, nil, errors.New("a slug or project change must be the last action")
		}
		if rule := action.BranchRestriction; rule != nil && action.writes() && rule.ID > 0 {
			if rules[rule.ID] {
				return nil, nil, fmt.Errorf("branch restriction %d can only be changed once per batch", rule.ID)
			}
			rules[rule.ID] = true
		}
		if hook := action.Webhook; hook != nil && action.writes() && hook.UUID != "" {
			id := strings.ToLower(strings.Trim(hook.UUID, "{}"))
			if hooks[id] {
				return nil, nil, fmt.Errorf("webhook %q can only be changed once per batch", hook.UUID)
			}
			hooks[id] = true
		}
	}
	return repos, actions, nil
}

func repositorySecretFields(actions []repositoryAction, batch bool) []secretField {
	var fields []secretField
	for i, action := range actions {
		if action.Webhook == nil || !action.Webhook.SetSecret {
			continue
		}
		field := secretField{Name: "secret", Label: "Signing secret"}
		if batch {
			field.Name = fmt.Sprintf("webhook_secret_%d", i+1)
			field.Label = fmt.Sprintf("Signing secret for action %d, shared by its selected repositories", i+1)
		}
		fields = append(fields, field)
	}
	return fields
}

func (t *tools) prepareRepositoryPlan(ctx context.Context, repos []string, actions []repositoryAction, fields []secretField, batch bool) (repositoryPlan, error) {
	var plan repositoryPlan
	// Count each repeated action before building the full browser preview.
	remaining := maxRepositoryApprovalBytes
	seen := make(map[string]bool)
	for _, repo := range repos {
		before, err := t.bb.RepositorySettings(ctx, repo)
		if err != nil {
			return repositoryPlan{}, fmt.Errorf("repository %q: %w", repo, err)
		}
		if batch {
			if !bitbucket.ValidRepositoryUUID(before.UUID) {
				return repositoryPlan{}, fmt.Errorf("bitbucket returned no usable UUID for repository %q", repo)
			}
			uuid := strings.ToLower(before.UUID)
			if seen[uuid] {
				return repositoryPlan{}, fmt.Errorf("repository %q resolves to a repository already included in the batch", repo)
			}
			seen[uuid] = true
		}
		target := repositoryTargetApproval{Repo: repo, UUID: before.UUID, Project: before.Project.Key, ProjectUUID: before.Project.UUID}
		remaining -= len(target.Repo) + len(target.UUID) + len(target.Project) + len(target.ProjectUUID)
		secretIndex := 0
		for i, action := range actions {
			operation, err := t.planRepositoryAction(ctx, repo, before, action)
			if err != nil {
				return repositoryPlan{}, fmt.Errorf("repository %q action %d: %w", repo, i+1, err)
			}
			operation.approval.repositoryAction = action
			if action.Webhook != nil && action.Webhook.SetSecret {
				operation.approval.SigningSecretField = fields[secretIndex].Name
				secretIndex++
			}
			if batch {
				details, err := json.Marshal(operation.approval)
				if err != nil {
					return repositoryPlan{}, err
				}
				remaining -= len(details) + len(operation.hiddenURL)
				if remaining < 0 {
					return repositoryPlan{}, errors.New("repository batch approval exceeds 512 KiB; split the batch")
				}
			}
			target.Actions = append(target.Actions, operation.approval)
			plan.operations = append(plan.operations, operation)
			plan.webhookURLs = append(plan.webhookURLs, operation.hiddenURL)
		}
		plan.targets = append(plan.targets, target)
	}
	return plan, nil
}

func (t *tools) planRepositoryAction(ctx context.Context, repo string, before bitbucket.Repository, action repositoryAction) (repositoryOperation, error) {
	switch {
	case !action.writes():
		return repositoryOperation{apply: func(ctx context.Context, until time.Time, _ string) (repositoryActionResult, error) {
			ctx, cancel := context.WithDeadline(ctx, until)
			defer cancel()
			result, err := t.readRepositoryAction(ctx, repo, action, &before)
			if err != nil {
				return repositoryActionResult{}, err
			}
			return result, nil
		}}, nil
	case action.BranchRestriction != nil:
		return t.planBranchRestriction(ctx, repo, before, *action.BranchRestriction)
	case action.Webhook != nil:
		return t.planWebhook(ctx, repo, before, *action.Webhook)
	default:
		return t.planRepositorySettings(ctx, repo, before, action)
	}
}

func (t *tools) manageRepositoryOperations(ctx context.Context, req *mcp.CallToolRequest, in manageRepositoryInput) (*mcp.CallToolResult, manageRepositoryResult, error) {
	out := manageRepositoryResult{repositoryActionResult: repositoryActionResult{ContentTrust: "untrusted"}}
	page, err := t.claimSecrets(req, in)
	if err != nil {
		return nil, out, err
	}
	defer page.close()
	repos, actions, err := in.operationInputs()
	if err != nil {
		return nil, out, err
	}

	fields := repositorySecretFields(actions, in.batch())
	checkCtx := ctx
	if page != nil {
		var cancel context.CancelFunc
		checkCtx, cancel = context.WithDeadline(ctx, page.page.info.Until)
		defer cancel()
	}
	plan, err := t.prepareRepositoryPlan(checkCtx, repos, actions, fields, in.batch())
	if err != nil {
		return nil, out, err
	}
	details, err := json.MarshalIndent(plan.targets, "", "  ")
	if err != nil {
		return nil, out, err
	}
	if in.batch() && len(details) > maxRepositoryApprovalBytes {
		return nil, out, errors.New("repository batch approval exceeds 512 KiB; split the batch")
	}
	if page == nil {
		// A form may percent-encode every byte of a four-byte character. Leave room
		// for each field name and the approval checkbox in addition to the values.
		formSizeLimit := int64(len(fields)*(maxWebhookSecretRunes*utf8.UTFMax*3+64) + 64)
		res, err := t.askSecrets(ctx, req, in, secretPageInfo{
			Title:         "Confirm repository operations",
			Need:          "Review the repositories and the actions in their execution order.",
			Outcome:       "Confirmation authorizes the displayed actions. If an action fails, earlier changes remain applied.",
			Arguments:     string(details),
			Fields:        fields,
			webhookURLs:   plan.webhookURLs,
			formSizeLimit: formSizeLimit,
		})
		return res, out, err
	}
	if !page.automatic && (page.page.info.Arguments != string(details) || !slices.Equal(page.page.info.webhookURLs, plan.webhookURLs)) {
		if !in.batch() && in.Webhook != nil {
			return nil, out, errors.New("webhook changed while awaiting approval; call the tool again")
		}
		return nil, out, errors.New("repository operations changed while awaiting approval; call the tool again")
	}
	if !accepted(req, page) {
		return nil, out, errors.New("the repository operations were not approved")
	}
	values, err := t.waitSecrets(ctx, req, page)
	if err != nil {
		return nil, out, err
	}
	for _, field := range fields {
		if n := utf8.RuneCountInString(values[field.Name]); n < 1 || n > maxWebhookSecretRunes {
			return nil, out, errors.New("webhook signing secret must contain 1 to 128 characters")
		}
	}
	if !in.batch() {
		if err := approvalLive(page); err != nil {
			return nil, out, err
		}
		result, err := plan.operations[0].apply(ctx, page.page.info.Until, values["secret"])
		return nil, manageRepositoryResult{repositoryActionResult: result}, err
	}

	if !page.automatic {
		// A retry can start before the browser form is submitted. Recheck every target before
		// the first write, so a changed later repository does not leave an avoidable partial batch.
		current, err := t.prepareRepositoryPlan(checkCtx, repos, actions, fields, true)
		if err != nil {
			return nil, out, err
		}
		if !reflect.DeepEqual(plan.targets, current.targets) || !slices.Equal(plan.webhookURLs, current.webhookURLs) {
			return nil, out, errors.New("repository operations changed while awaiting approval; call the tool again")
		}
	}
	return applyRepositoryBatch(ctx, checkCtx, page.page.info.Until, plan, values)
}

func applyRepositoryBatch(ctx, checkCtx context.Context, until time.Time, plan repositoryPlan, values map[string]string) (*mcp.CallToolResult, manageRepositoryResult, error) {
	out := manageRepositoryResult{
		repositoryActionResult: repositoryActionResult{ContentTrust: "untrusted"},
		Status:                 "completed",
		Results:                make([]repositoryBatchEntry, 0, len(plan.operations)),
	}
	for _, target := range plan.targets {
		for i := range target.Actions {
			out.Results = append(out.Results, repositoryBatchEntry{Repo: target.Repo, Action: i + 1, Status: "skipped"})
		}
	}
	remaining := maxRepositoryResultBytes
	for i, operation := range plan.operations {
		entry := &out.Results[i]
		err := checkCtx.Err()
		var result repositoryActionResult
		if err == nil {
			result, err = operation.apply(ctx, until, values[operation.approval.SigningSecretField])
		}
		if result != (repositoryActionResult{}) {
			if data, err := json.Marshal(result); err == nil && len(data) <= remaining {
				entry.Result = &result
				remaining -= len(data)
			} else {
				entry.ResultOmitted = true
			}
		}
		if err != nil {
			entry.Status, entry.Error = "failed", err.Error()
			out.Status = "failed"
			if i > 0 || result != (repositoryActionResult{}) {
				out.Status = "partial"
			}
			return &mcp.CallToolResult{IsError: true}, out, nil
		}
		entry.Status = "completed"
	}
	return nil, out, nil
}
