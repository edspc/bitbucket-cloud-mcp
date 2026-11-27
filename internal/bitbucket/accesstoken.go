package bitbucket

import (
	"context"
	"net/http"
	"slices"
	"strings"
)

// AccessTokensPage is where a repository access token is created; Bitbucket has no API for it.
func (c *Client) AccessTokensPage(repo string) string {
	return c.repoWebURL(repo, "admin", "access-tokens")
}

// CheckTokenAccess probes read access to the approved source repository. The requested
// permissions choose the endpoint; success does not verify the token type or actual scopes.
func (c *Client) CheckTokenAccess(ctx context.Context, repo string, source Repository, token string, permissions []string) error {
	if err := c.checkRepositoryIdentity(ctx, repo, source); err != nil {
		return err
	}
	bearer := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
	return c.sendUnchecked(ctx, http.MethodGet, APIURL+c.tokenProbePath(source.UUID, permissions), bearer, nil, nil)
}

// tokenProbePath picks a read endpoint of repo that the permissions grant; each write or admin
// permission includes the read permission of its kind, and pull requests include the repository.
func (c *Client) tokenProbePath(repo string, permissions []string) string {
	has := func(kind string) bool {
		return slices.ContainsFunc(permissions, func(p string) bool { return strings.HasPrefix(p, kind) })
	}
	switch {
	case has("repository"), has("pullrequest"):
		return c.repoPath(repo)
	case has("pipeline"):
		return c.repoPath(repo, "pipelines") + "?pagelen=1"
	case has("webhook"):
		return c.repoPath(repo, "hooks")
	}
	return ""
}
