package tools

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

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
			_, _, err := repositoryListPage(in.Page, in.Limit)
			return err
		}
	case "get", "delete":
		if in.ID > 0 && in.Rule == nil {
			return nil
		}
	case "create":
		if in.ID == 0 && in.Rule != nil {
			_, err := in.fieldsForWrite(nil)
			return err
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

func (t *tools) getBranchRestrictions(ctx context.Context, _ *mcp.CallToolRequest, in getBranchRestrictionsInput) (*mcp.CallToolResult, repositoryActionResult, error) {
	if err := t.policy.checkRepo(in.Repo); err != nil {
		return nil, repositoryActionResult{}, err
	}
	action := "list"
	if in.ID != 0 {
		action = "get"
	}
	args := branchRestrictionInput{Action: action, ID: in.ID, Page: in.Page, Limit: in.Limit}
	if err := args.validate(); err != nil {
		return nil, repositoryActionResult{}, err
	}
	out, err := t.readBranchRestriction(ctx, in.Repo, args, nil)
	return nil, out, err
}

func (t *tools) readBranchRestriction(ctx context.Context, repo string, args branchRestrictionInput, before *bitbucket.Repository) (repositoryActionResult, error) {
	out := repositoryActionResult{ContentTrust: "untrusted"}
	if args.Action == "get" {
		var rule bitbucket.BranchRule
		var err error
		if before == nil {
			rule, err = t.bb.BranchRule(ctx, repo, args.ID)
		} else {
			rule, err = t.bb.PinnedBranchRule(ctx, repo, *before, args.ID)
		}
		out.BranchRestriction = &rule
		return out, err
	}
	page, limit, err := repositoryListPage(args.Page, args.Limit)
	if err != nil {
		return out, err
	}
	rules, err := t.bb.ListBranchRules(ctx, repo, page, limit, before)
	if rules.Values == nil {
		rules.Values = []bitbucket.BranchRule{}
	}
	out.BranchRestrictions = &rules.Values
	out.NextPage, out.Total = nextPage(page, rules.More), rules.Total
	return out, err
}

func (in branchRestrictionInput) fieldsForWrite(before *bitbucket.BranchRule) (bitbucket.BranchRuleFields, error) {
	if in.Rule == nil {
		return bitbucket.BranchRuleFields{}, nil
	}
	fields := *in.Rule
	if before != nil {
		return fields, validateBranchRule(fields, before.Kind)
	}
	if fields.Kind == "" {
		return fields, errors.New("kind is required to create a branch restriction")
	}
	if fields.BranchMatchKind == "" {
		fields.BranchMatchKind = "glob"
	}
	switch fields.BranchMatchKind {
	case "glob":
		if fields.Pattern == nil || strings.TrimSpace(*fields.Pattern) == "" || fields.BranchType != "" {
			return fields, errors.New("glob rules require a nonempty pattern and no branch_type")
		}
	case "branching_model":
		if fields.BranchType == "" || fields.Pattern != nil && *fields.Pattern != "" {
			return fields, errors.New("branching_model rules require branch_type and an empty pattern")
		}
		if fields.Pattern == nil {
			empty := ""
			fields.Pattern = &empty
		}
	default:
		return fields, errors.New("branch_match_kind must be glob or branching_model")
	}
	if (fields.Kind == "push" || fields.Kind == "restrict_merges") && (fields.Users == nil || fields.Groups == nil) {
		return fields, errors.New("creating push or restrict_merges requires explicit users and groups arrays; empty arrays grant no user or group exceptions")
	}
	return fields, validateBranchRule(fields, "")
}

func (t *tools) planBranchRestriction(ctx context.Context, repo string, beforeRepo bitbucket.Repository, args branchRestrictionInput) (repositoryOperation, error) {
	var before *bitbucket.BranchRule
	if args.ID > 0 {
		rule, err := t.bb.BranchRule(ctx, repo, args.ID)
		if err != nil {
			return repositoryOperation{}, err
		}
		before = &rule
	}
	fields, err := args.fieldsForWrite(before)
	if err != nil {
		return repositoryOperation{}, err
	}
	return repositoryOperation{
		approval: repositoryActionApproval{CurrentRule: before},
		apply: func(ctx context.Context, until time.Time, _ string) (repositoryActionResult, error) {
			writeCtx := bitbucket.WithWriteDeadline(ctx, until)
			if before != nil {
				checkCtx, cancel := context.WithDeadline(writeCtx, until)
				defer cancel()
				current, err := t.bb.PinnedBranchRule(checkCtx, repo, beforeRepo, args.ID)
				if err != nil {
					return repositoryActionResult{}, err
				}
				if !reflect.DeepEqual(current, *before) {
					return repositoryActionResult{}, errors.New("branch restriction changed; call the tool again")
				}
			}
			return t.writeBranchRestriction(ctx, writeCtx, repo, beforeRepo, args, fields)
		},
	}, nil
}

func (t *tools) writeBranchRestriction(ctx, writeCtx context.Context, repo string, beforeRepo bitbucket.Repository, args branchRestrictionInput, fields bitbucket.BranchRuleFields) (repositoryActionResult, error) {
	out := repositoryActionResult{}
	var rule bitbucket.BranchRule
	var err error
	switch args.Action {
	case "create":
		rule, err = t.bb.CreateBranchRule(writeCtx, repo, beforeRepo, fields)
	case "update":
		rule, err = t.bb.UpdateBranchRule(writeCtx, repo, beforeRepo, args.ID, fields)
	case "delete":
		if err = t.bb.DeleteBranchRule(writeCtx, repo, beforeRepo, args.ID); err != nil {
			return out, err
		}
		return repositoryActionResult{ContentTrust: "untrusted", DeletedBranchRestrictionID: args.ID}, nil
	}
	if err != nil {
		return out, err
	}
	out.ContentTrust, out.BranchRestriction = "untrusted", &rule
	if fields.PipelinesSourceBranches == nil {
		return out, nil
	}
	saved, err := t.bb.PinnedBranchRule(ctx, repo, beforeRepo, rule.ID)
	if err != nil {
		return out, fmt.Errorf("branch rule %d was written but pipeline source branches could not be verified: %w", rule.ID, err)
	}
	want := slices.Clone(*fields.PipelinesSourceBranches)
	if saved.PipelinesSourceBranches == nil {
		return out, fmt.Errorf("branch rule %d was written but Bitbucket omitted pipeline source branches; the requested change could not be verified", rule.ID)
	}
	got := slices.Clone(*saved.PipelinesSourceBranches)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		return out, fmt.Errorf("branch rule %d was written but Bitbucket did not return the requested pipeline source branches", rule.ID)
	}
	out.BranchRestriction = &saved
	return out, nil
}
