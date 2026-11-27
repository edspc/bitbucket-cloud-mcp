package bitbucket

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ValidRepositorySlug reports whether slug can safely name one repository path segment.
func ValidRepositorySlug(slug string) bool {
	return slug != "" && slug != "." && slug != ".." &&
		strings.Trim(slug, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == ""
}

func (c *Client) RepositoryUUID(ctx context.Context, repo string) (string, error) {
	var metadata struct {
		UUID string `json:"uuid"`
	}
	if err := c.call(ctx, http.MethodGet, c.repoPath(repo)+"?fields=uuid", nil, &metadata); err != nil {
		return "", err
	}
	if metadata.UUID == "" {
		return "", errors.New("bitbucket returned no repository UUID")
	}
	return metadata.UUID, nil
}

type RepositoryUpdate struct {
	Name        string             `json:"name,omitempty"`
	Project     *RepositoryProject `json:"project,omitempty"`
	Description *string            `json:"description,omitempty"`
	Language    *string            `json:"language,omitempty"`
}

type RepositoryProject struct {
	Key  string `json:"key"`
	UUID string `json:"uuid,omitempty"`
}

func (c *Client) RepositorySettings(ctx context.Context, repo string) (Repository, error) {
	var out Repository
	err := c.call(ctx, http.MethodGet, c.repoPath(repo)+"?fields=uuid,slug,name,description,language,is_private,mainbranch.name,updated_on,project.key,project.uuid,project.name", nil, &out)
	if err == nil && !c.allowsRepositoryProject(repo, out.Project.Key, out.Project.UUID) {
		return Repository{}, errors.New("repository project is not allowed by the server policy")
	}
	return out, err
}

func (c *Client) RepositoryProject(ctx context.Context, key string) (RepositoryProject, error) {
	var out RepositoryProject
	path := escapedPath("workspaces", c.cfg.Workspace, "projects", key) + "?fields=key,uuid"
	err := c.sendUnchecked(ctx, http.MethodGet, APIURL+path, c.basicAuth, nil, &out)
	return out, err
}

// checkRepositoryIdentity reauthorizes the slug and ensures it still names the
// repository and project that were presented for approval.
func (c *Client) checkRepositoryIdentity(ctx context.Context, repo string, before Repository) error {
	matched, err := regexp.MatchString(`^\{[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}\}$`, before.UUID)
	if err != nil || !matched {
		return errors.New("bitbucket returned no usable repository UUID")
	}
	current, err := c.RepositorySettings(ctx, repo)
	if err != nil {
		return err
	}
	if current.UUID != before.UUID || current.Project.Key != before.Project.Key || current.Project.UUID != before.Project.UUID {
		return errors.New("repository identity or project changed; call the tool again")
	}
	return ctx.Err()
}

func repositoryCheckContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if until, ok := ctx.Value(writeDeadlineKey{}).(time.Time); ok {
		return context.WithDeadline(ctx, until)
	}
	return ctx, func() {}
}

func (c *Client) UpdateRepository(ctx context.Context, repo string, before Repository, update RepositoryUpdate, expectedProjectUUID string) (Repository, error) {
	checkCtx, cancel := repositoryCheckContext(ctx)
	defer cancel()
	if err := c.checkRepositoryIdentity(checkCtx, repo, before); err != nil {
		return Repository{}, err
	}
	if update.Project != nil && update.Project.Key != before.Project.Key && expectedProjectUUID != "" {
		project, err := c.RepositoryProject(checkCtx, update.Project.Key)
		if err != nil {
			return Repository{}, err
		}
		if project.Key != update.Project.Key || project.UUID != expectedProjectUUID {
			return Repository{}, errors.New("destination project identity changed; call the tool again")
		}
	}
	if err := checkCtx.Err(); err != nil {
		return Repository{}, err
	}
	// The UUID keeps the write on the approved repository if its slug changes.
	var out Repository
	err := c.sendUnchecked(ctx, http.MethodPut, APIURL+c.repoPath(before.UUID), c.basicAuth, update, &out)
	return out, err
}
