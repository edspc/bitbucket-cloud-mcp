package bitbucket

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

// CodeFilter deliberately exposes no namespace, scope or arbitrary filter tree.
type CodeFilter struct {
	Attribute string       `json:"attribute,omitempty"`
	Eq        string       `json:"eq,omitempty"`
	Wildcard  string       `json:"wildcard,omitempty"`
	And       []CodeFilter `json:"and,omitempty"`
	Or        []CodeFilter `json:"or,omitempty"`
}

type CodeQuery struct {
	SearchTerm string      `json:"search_term"`
	Filters    *CodeFilter `json:"filters,omitempty"`
}

type CodePagination struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type CodeSearchRequest struct {
	Query  CodeQuery `json:"query"`
	Config struct {
		MaxResults int `json:"max_results"`
	} `json:"config"`
	RequestID  string          `json:"request_id,omitempty"`
	Pagination *CodePagination `json:"pagination,omitempty"`
}

type CodeChunk struct {
	SourceCode string `json:"source_code"`
	LineRange  struct {
		Start int `json:"start"`
		End   int `json:"end"`
	} `json:"line_range"`
	MatchedLineNumbers []int    `json:"matched_line_numbers"`
	MatchedRanges      [][3]int `json:"matched_ranges"`
}

type CodeFile struct {
	Path            string      `json:"path"`
	Name            string      `json:"name"`
	Language        *string     `json:"language"`
	URL             string      `json:"url"`
	Version         string      `json:"version"`
	BranchOrTagName string      `json:"branch_or_tag_name,omitempty"`
	IsDefaultBranch *bool       `json:"is_default_branch,omitempty"`
	MatchedChunks   []CodeChunk `json:"matched_chunks"`
}

type CodeSearchResult struct {
	ID           string         `json:"id"`
	Pagination   CodePagination `json:"pagination"`
	MatchedFiles []CodeFile     `json:"matched_files"`
	Metadata     *struct {
		TotalCount int `json:"total_count"`
	} `json:"metadata,omitempty"`
}

// SearchCode applies server policy before the workspace request and checks returned
// repositories again: the workspace endpoint cannot go through checkProjectURL.
func (c *Client) SearchCode(ctx context.Context, repo string, in CodeSearchRequest) (CodeSearchResult, error) {
	var out CodeSearchResult
	if strings.TrimSpace(in.Query.SearchTerm) == "" || utf8.RuneCountInString(in.Query.SearchTerm) > 1024 {
		return out, errors.New("query must be non-blank and at most 1024 characters")
	}
	if in.Config.MaxResults < 1 || in.Config.MaxResults > 100 {
		return out, errors.New("limit must be between 1 and 100")
	}
	if (in.Pagination != nil) != (in.RequestID != "") || in.Pagination != nil && in.Pagination.NextCursor == "" {
		return out, errors.New("request_id and next_cursor must be supplied together")
	}
	if err := validateCodeFilter(in.Query.Filters, repo); err != nil {
		return out, err
	}
	var scope []string
	if repo != "" {
		if !validCodeRepository(repo) {
			return out, errors.New("repo must be a repository slug, without path segments")
		}
		if c.allowedProjects == nil && c.allowedRepos != nil && !slices.Contains(c.allowedRepos, repo) {
			return out, errors.New("repository is not allowed by the server policy")
		}
		if err := c.CheckRepositoryProject(ctx, repo); err != nil {
			return out, err
		}
	} else if c.allowedProjects != nil || c.allowedRepos != nil {
		var err error
		scope, err = c.codeSearchScope(ctx)
		if err != nil {
			return out, err
		}
		if len(scope) == 0 {
			return CodeSearchResult{MatchedFiles: []CodeFile{}}, nil
		}
		allowed := CodeFilter{Or: make([]CodeFilter, 0, len(scope))}
		for _, slug := range scope {
			allowed.Or = append(allowed.Or, CodeFilter{Attribute: "repository", Eq: slug})
		}
		// Keep at most three boolean levels around a leaf, including caller filters.
		if in.Query.Filters == nil {
			in.Query.Filters = &allowed
		} else {
			in.Query.Filters = &CodeFilter{And: []CodeFilter{allowed, *in.Query.Filters}}
		}
	}
	path := escapedPath("workspaces", c.cfg.Workspace, "codesearch")
	if err := c.sendUnchecked(ctx, http.MethodPost, APIURL+path, c.basicAuth, in, &out); err != nil {
		return CodeSearchResult{}, err
	}
	// Reject a wider response rather than returning unrelated code or metadata.
	checked := make(map[string]bool)
	for _, f := range out.MatchedFiles {
		u, err := url.Parse(f.URL)
		parts := []string(nil)
		if err == nil {
			parts = strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		}
		if err != nil || u.Scheme != "https" || u.Host != "bitbucket.org" || u.User != nil || len(parts) < 5 || parts[0] != c.cfg.Workspace || parts[2] != "src" || repo != "" && parts[1] != repo {
			return CodeSearchResult{}, errors.New("code search returned a file outside the requested workspace or repository")
		}
		slug := parts[1]
		if !validCodeRepository(slug) || scope != nil && !slices.Contains(scope, slug) {
			return CodeSearchResult{}, errors.New("code search returned a repository outside the server policy")
		}
		if !checked[slug] {
			// Project membership may change while the search is in flight.
			if err := c.CheckRepositoryProject(ctx, slug); err != nil {
				return CodeSearchResult{}, err
			}
			checked[slug] = true
		}
	}
	if c.allowedProjects != nil || c.allowedRepos != nil {
		out.Metadata = nil
	}
	if out.MatchedFiles == nil {
		out.MatchedFiles = []CodeFile{}
	}
	return out, nil
}

func validateCodeFilter(f *CodeFilter, repo string) error {
	var filters []CodeFilter
	if f != nil {
		filters = []CodeFilter{*f}
		if len(f.And) > 0 {
			if f.Attribute != "" || f.Eq != "" || f.Wildcard != "" || len(f.Or) > 0 {
				return errors.New("invalid code search filters")
			}
			filters = f.And
		}
	}
	foundRepo := false
	for _, v := range filters {
		if len(v.And) > 0 || len(v.Or) > 0 || (v.Eq == "") == (v.Wildcard == "") {
			return errors.New("invalid code search filter")
		}
		switch v.Attribute {
		case "repository":
			if repo == "" || v.Eq != repo || v.Wildcard != "" || foundRepo {
				return errors.New("invalid repository filter")
			}
			foundRepo = true
		case "language", "extension":
			if v.Wildcard != "" {
				return errors.New("wildcard is supported only for path")
			}
		case "path":
		default:
			return errors.New("unsupported code search filter")
		}
	}
	if repo != "" && !foundRepo {
		return errors.New("code search requires an exact repository filter")
	}
	return nil
}
