package bitbucket

import (
	"context"
	"errors"
	"net/http"
)

func (c *Client) UserUUID(ctx context.Context, user string) (string, error) {
	path := "/user"
	if user != "me" {
		path = escapedPath("users", user)
	}
	var result struct {
		UUID string `json:"uuid"`
	}
	if err := c.sendUnchecked(ctx, http.MethodGet, APIURL+path+"?fields=uuid", c.basicAuth, nil, &result); err != nil {
		return "", err
	}
	if result.UUID == "" {
		return "", errors.New("bitbucket returned no user UUID")
	}
	return result.UUID, nil
}
