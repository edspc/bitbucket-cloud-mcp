package bitbucket

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
)

// Variable is a pipeline variable; Value is empty for secured variables read back from the API.
type Variable struct {
	UUID    string `json:"uuid,omitempty"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Secured bool   `json:"secured"`
}

type Environment struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// VariableScope selects where a variable lives: the whole workspace when Repo is empty, the
// whole repository, or one deployment environment of it when EnvironmentUUID is set.
type VariableScope struct {
	Repo            string `json:"repo,omitempty"`
	EnvironmentUUID string `json:"environment_uuid,omitempty"`
}

func (c *Client) variablesPath(s VariableScope, parts ...string) string {
	switch {
	case s.Repo == "":
		return escapedPath(append([]string{"workspaces", c.cfg.Workspace, "pipelines-config", "variables"}, parts...)...)
	case s.EnvironmentUUID == "":
		return c.repoPath(s.Repo, append([]string{"pipelines_config", "variables"}, parts...)...)
	}
	return c.repoPath(s.Repo, append([]string{"deployments_config", "environments", s.EnvironmentUUID, "variables"}, parts...)...)
}

func (c *Client) FindVariable(ctx context.Context, s VariableScope, key string) (*Variable, error) {
	var found *Variable
	err := walkPages(ctx, c, c.variablesPath(s), func(values []Variable) bool {
		if i := slices.IndexFunc(values, func(v Variable) bool { return v.Key == key }); i >= 0 {
			found = &values[i]
		}
		return found == nil
	})
	return found, err
}

func (c *Client) ListVariablesPage(ctx context.Context, s VariableScope, page, limit int) (Page[Variable], error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "pagelen": {strconv.Itoa(limit)}}
	p, err := readPage[Variable](ctx, c, APIURL+c.variablesPath(s)+"?"+q.Encode(), limit)
	return Page[Variable]{Values: p.Values, Total: p.Size, More: p.Next != ""}, err
}

func (c *Client) CreateVariable(ctx context.Context, s VariableScope, v Variable) error {
	return c.call(ctx, http.MethodPost, c.variablesPath(s), v, nil)
}

func (c *Client) UpdateVariable(ctx context.Context, s VariableScope, v Variable) error {
	return c.call(ctx, http.MethodPut, c.variablesPath(s, v.UUID), v, nil)
}

// WriteLinkedVariable reauthorizes both repositories before writing the token to the approved
// target UUID. A new repository taking over the target slug cannot receive the token.
func (c *Client) WriteLinkedVariable(ctx context.Context, sourceRepo string, source, target Repository, s VariableScope, v Variable, existing *Variable) (string, error) {
	checkCtx, cancel := repositoryCheckContext(ctx)
	defer cancel()
	if err := c.checkRepositoryIdentity(checkCtx, sourceRepo, source); err != nil {
		return "", err
	}
	if err := c.checkRepositoryIdentity(checkCtx, s.Repo, target); err != nil {
		return "", err
	}
	if err := checkCtx.Err(); err != nil {
		return "", err
	}
	s.Repo = target.UUID
	if existing == nil {
		return "created", c.sendUnchecked(ctx, http.MethodPost, APIURL+c.variablesPath(s), c.basicAuth, v, nil)
	}
	if existing.UUID == "" {
		return "", errors.New("bitbucket returned no UUID for the variable to replace")
	}
	v.UUID = existing.UUID
	return "updated", c.sendUnchecked(ctx, http.MethodPut, APIURL+c.variablesPath(s, v.UUID), c.basicAuth, v, nil)
}

func (c *Client) DeleteVariable(ctx context.Context, s VariableScope, uuid string) error {
	return c.call(ctx, http.MethodDelete, c.variablesPath(s, uuid), nil, nil)
}

func (c *Client) ListEnvironments(ctx context.Context, repo string) ([]Environment, error) {
	return list[Environment](ctx, c, c.repoPath(repo, "environments"))
}
