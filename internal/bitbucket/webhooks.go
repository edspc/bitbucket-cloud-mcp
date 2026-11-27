package bitbucket

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Webhook struct {
	UUID        string   `json:"uuid"`
	URL         string   `json:"url"`
	Description string   `json:"description"`
	Active      bool     `json:"active"`
	Events      []string `json:"events"`
	SecretSet   bool     `json:"secret_set"`
	CreatedAt   string   `json:"created_at,omitempty"`
}

type WebhookSettings struct {
	URL         *string   `json:"url,omitempty"`
	Description *string   `json:"description,omitempty"`
	Active      *bool     `json:"active,omitempty"`
	Events      *[]string `json:"events,omitempty"`
}

type WebhookWrite struct {
	WebhookSettings
	Secret *string `json:"secret,omitempty"`
}

// PublicWebhookURL returns the origin because webhook credentials can be part of the path.
func PublicWebhookURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	origin := url.URL{Scheme: u.Scheme, Host: u.Host}
	return origin.String()
}

func isWebhookAPIPath(path string) bool {
	path = strings.TrimPrefix(path, "/2.0/")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	return len(parts) >= 4 && parts[0] == "repositories" && parts[3] == "hooks" ||
		len(parts) >= 3 && parts[0] == "workspaces" && parts[2] == "hooks"
}

func (c *Client) ListWebhooks(ctx context.Context, repo string, page, limit int) (Page[Webhook], error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "pagelen": {strconv.Itoa(limit)}}
	result, err := readPage[Webhook](ctx, c, APIURL+c.repoPath(repo, "hooks")+"?"+q.Encode(), limit)
	return Page[Webhook]{Values: result.Values, More: result.Next != "", Total: result.Size}, err
}

func (c *Client) Webhook(ctx context.Context, repo, id string) (Webhook, error) {
	var hook Webhook
	err := c.call(ctx, http.MethodGet, c.repoPath(repo, "hooks", id), nil, &hook)
	return hook, err
}

func (c *Client) WriteWebhook(ctx context.Context, repo string, before Repository, id string, write *WebhookWrite) (Webhook, error) {
	checkCtx, cancel := repositoryCheckContext(ctx)
	defer cancel()
	if err := c.checkRepositoryIdentity(checkCtx, repo, before); err != nil {
		return Webhook{}, err
	}
	path := c.repoPath(before.UUID, "hooks")
	var hook Webhook
	switch {
	case id == "":
		err := c.sendUnchecked(ctx, http.MethodPost, APIURL+path, c.basicAuth, *write, &hook)
		return hook, err
	case write == nil:
		err := c.sendUnchecked(ctx, http.MethodDelete, APIURL+path+escapedPath(id), c.basicAuth, nil, nil)
		return hook, err
	default:
		err := c.sendUnchecked(ctx, http.MethodPut, APIURL+path+escapedPath(id), c.basicAuth, *write, &hook)
		return hook, err
	}
}
