package bitbucket

import (
	"context"
	"errors"
	"slices"
	"strings"
)

func validCodeRepository(slug string) bool {
	return slug != "" && slug != "." && slug != ".." &&
		strings.Trim(slug, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == ""
}

// codeSearchScope resolves project UUIDs as well as keys to exact repository slugs.
// Sorting and deduplication keep the injected filter identical across cursor pages
// while the set of accessible repositories stays the same.
func (c *Client) codeSearchScope(ctx context.Context) ([]string, error) {
	scope := slices.Clone(c.allowedRepos)
	if len(c.allowedProjects) > 0 {
		filter := RepositoryFilter{Slugs: c.allowedRepos, Projects: c.allowedProjects}
		for page := 1; ; page++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			repos, err := c.ListRepositories(ctx, filter, page, 100)
			if err != nil {
				return nil, err
			}
			for _, r := range repos.Values {
				// Listings are external data and may be wider than the requested q.
				if r.FullName != "" && r.FullName != c.cfg.Workspace+"/"+r.Slug {
					continue
				}
				if slices.Contains(c.allowedRepos, r.Slug) ||
					r.Project.Key != "" && slices.Contains(c.allowedProjects, r.Project.Key) ||
					r.Project.UUID != "" && slices.Contains(c.allowedProjects, r.Project.UUID) {
					scope = append(scope, r.Slug)
				}
			}
			if !repos.More {
				break
			}
		}
	}
	for _, slug := range scope {
		if !validCodeRepository(slug) {
			return nil, errors.New("code search scope contains an invalid repository slug")
		}
	}
	slices.Sort(scope)
	return slices.Compact(scope), nil
}
