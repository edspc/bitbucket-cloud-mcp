package tools

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type branchRestrictionInput struct {
	Action string                      `json:"action" jsonschema:"list, get, create, update or delete"`
	ID     int                         `json:"id,omitempty" jsonschema:"rule ID for get, update or delete"`
	Page   int                         `json:"page,omitempty" jsonschema:"page for list, starting at 1"`
	Limit  int                         `json:"limit,omitempty" jsonschema:"list page size, 10 by default and at most 50"`
	Rule   *bitbucket.BranchRuleFields `json:"rule,omitempty" jsonschema:"rule fields for create or partial update; pipelines_source_branches applies to push rules"`
}

func (in branchRestrictionInput) validate() error {
	if in.Action != "list" && (in.Page != 0 || in.Limit != 0) {
		return errors.New("page and limit apply only to list")
	}
	switch in.Action {
	case "list":
		if in.ID == 0 && in.Rule == nil {
			return nil
		}
	case "get", "delete":
		if in.ID > 0 && in.Rule == nil {
			return nil
		}
	case "create":
		if in.ID == 0 && in.Rule != nil {
			return nil
		}
	case "update":
		if in.ID > 0 && in.Rule != nil && *in.Rule != (bitbucket.BranchRuleFields{}) {
			return nil
		}
	default:
		return errors.New("branch_restriction action must be list, get, create, update or delete")
	}
	return fmt.Errorf("invalid id or rule for branch_restriction action %q", in.Action)
}

func validateBranchRule(fields bitbucket.BranchRuleFields, kind string) error {
	if fields.Kind != "" {
		kind = fields.Kind
	}
	if fields.PipelinesSourceBranches != nil {
		if kind != "push" {
			return errors.New("pipelines_source_branches requires kind push")
		}
		for _, branch := range *fields.PipelinesSourceBranches {
			if branch == "" || strings.TrimSpace(branch) != branch {
				return errors.New("pipeline source branches must be nonempty branch names without surrounding whitespace")
			}
		}
	}
	if (fields.Users != nil || fields.Groups != nil) && kind != "push" && kind != "restrict_merges" {
		return errors.New("users and groups require kind push or restrict_merges")
	}
	return nil
}

type getBranchRestrictionsInput struct {
	Repo  string `json:"repo" jsonschema:"repository slug"`
	ID    int    `json:"id,omitempty" jsonschema:"rule ID; omit to list rules"`
	Page  int    `json:"page,omitempty" jsonschema:"list page starting at 1"`
	Limit int    `json:"limit,omitempty" jsonschema:"list page size, 10 by default and at most 50"`
}

func (t *tools) getBranchRestrictions(ctx context.Context, req *mcp.CallToolRequest, in getBranchRestrictionsInput) (*mcp.CallToolResult, manageRepositoryResult, error) {
	if err := t.policy.checkRepo(in.Repo); err != nil {
		return nil, manageRepositoryResult{}, err
	}
	action := "list"
	if in.ID != 0 {
		action = "get"
	}
	return t.manageBranchRestriction(ctx, req, manageRepositoryInput{
		Repo: in.Repo, BranchRestriction: &branchRestrictionInput{Action: action, ID: in.ID, Page: in.Page, Limit: in.Limit},
	})
}

func (t *tools) manageBranchRestriction(ctx context.Context, req *mcp.CallToolRequest, in manageRepositoryInput) (*mcp.CallToolResult, manageRepositoryResult, error) {
	args := *in.BranchRestriction
	out := manageRepositoryResult{ContentTrust: "untrusted"}
	if err := args.validate(); err != nil {
		return nil, out, err
	}
	switch args.Action {
	case "list":
		page, err := optionalPage(args.Page)
		if err != nil {
			return nil, out, err
		}
		limit, err := optionalCount("limit", args.Limit, defaultListLimit, maxListLimit)
		if err != nil {
			return nil, out, err
		}
		rules, err := t.bb.ListBranchRules(ctx, in.Repo, page, limit)
		if rules.Values == nil {
			rules.Values = []bitbucket.BranchRule{}
		}
		out.BranchRestrictions = &rules.Values
		out.NextPage, out.Total = nextPage(page, rules.More), rules.Total
		return nil, out, err
	case "get":
		rule, err := t.bb.BranchRule(ctx, in.Repo, args.ID)
		out.BranchRestriction = &rule
		return nil, out, err
	}

	page, err := t.claimSecrets(req, in)
	if err != nil {
		return nil, out, err
	}
	defer page.close()
	beforeRepo, err := t.bb.RepositorySettings(ctx, in.Repo)
	if err != nil {
		return nil, out, err
	}
	var before *bitbucket.BranchRule
	var fields bitbucket.BranchRuleFields
	if args.ID > 0 {
		rule, err := t.bb.BranchRule(ctx, in.Repo, args.ID)
		if err != nil {
			return nil, out, err
		}
		before = &rule
	}
	if args.Rule != nil {
		fields = *args.Rule
		kind := ""
		if before != nil {
			kind = before.Kind
		} else {
			if fields.Kind == "" {
				return nil, out, errors.New("kind is required to create a branch restriction")
			}
			if fields.BranchMatchKind == "" {
				fields.BranchMatchKind = "glob"
			}
			switch fields.BranchMatchKind {
			case "glob":
				if fields.Pattern == nil || strings.TrimSpace(*fields.Pattern) == "" || fields.BranchType != "" {
					return nil, out, errors.New("glob rules require a nonempty pattern and no branch_type")
				}
			case "branching_model":
				if fields.BranchType == "" || (fields.Pattern != nil && *fields.Pattern != "") {
					return nil, out, errors.New("branching_model rules require branch_type and an empty pattern")
				}
				if fields.Pattern == nil {
					empty := ""
					fields.Pattern = &empty
				}
			default:
				return nil, out, errors.New("branch_match_kind must be glob or branching_model")
			}
			if (fields.Kind == "push" || fields.Kind == "restrict_merges") && (fields.Users == nil || fields.Groups == nil) {
				return nil, out, errors.New("creating push or restrict_merges requires explicit users and groups arrays; empty arrays grant no user or group exceptions")
			}
		}
		if err = validateBranchRule(fields, kind); err != nil {
			return nil, out, err
		}
	}
	details := struct {
		manageRepositoryInput
		RepositoryUUID string                `json:"repository_uuid"`
		CurrentRule    *bitbucket.BranchRule `json:"current_rule,omitempty"`
	}{in, beforeRepo.UUID, before}
	res, approved, err := t.confirmChange(ctx, req, in, page, details)
	if err != nil || res != nil {
		return res, out, err
	}
	if !approved {
		return nil, out, errors.New("branch restriction change was not approved")
	}
	checkCtx, cancel := context.WithDeadline(ctx, page.page.info.Until)
	defer cancel()
	if before != nil {
		current, err := t.bb.PinnedBranchRule(checkCtx, in.Repo, beforeRepo, args.ID)
		if err != nil {
			return nil, out, err
		}
		if !reflect.DeepEqual(current, *before) {
			return nil, out, errors.New("branch restriction changed; call the tool again")
		}
	}
	if err = approvalLive(page); err != nil {
		return nil, out, err
	}
	writeCtx := bitbucket.WithWriteDeadline(ctx, page.page.info.Until)
	var rule bitbucket.BranchRule
	switch args.Action {
	case "create":
		rule, err = t.bb.CreateBranchRule(writeCtx, in.Repo, beforeRepo, fields)
	case "update":
		rule, err = t.bb.UpdateBranchRule(writeCtx, in.Repo, beforeRepo, args.ID, fields)
	case "delete":
		if err = t.bb.DeleteBranchRule(writeCtx, in.Repo, beforeRepo, args.ID); err != nil {
			return nil, out, err
		}
		out.DeletedBranchRestrictionID = args.ID
		return nil, out, nil
	}
	if err != nil {
		return nil, out, err
	}
	if fields.PipelinesSourceBranches != nil {
		saved, err := t.bb.PinnedBranchRule(ctx, in.Repo, beforeRepo, rule.ID)
		if err != nil {
			return nil, out, fmt.Errorf("branch rule %d was written but pipeline source branches could not be verified: %w", rule.ID, err)
		}
		want := slices.Clone(*fields.PipelinesSourceBranches)
		if saved.PipelinesSourceBranches == nil {
			return nil, out, fmt.Errorf("branch rule %d was written but Bitbucket omitted pipeline source branches; the requested change could not be verified", rule.ID)
		}
		got := slices.Clone(*saved.PipelinesSourceBranches)
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			return nil, out, fmt.Errorf("branch rule %d was written but Bitbucket did not return the requested pipeline source branches", rule.ID)
		}
		rule = saved
	}
	out.BranchRestriction = &rule
	return nil, out, nil
}
