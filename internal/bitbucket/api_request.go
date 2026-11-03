package bitbucket

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxAPIRequestBytes  = 64 << 10
	maxAPIResponseBytes = 40 << 10
)

type APIRequest struct {
	Method string              `json:"method" jsonschema:"uppercase HTTP method: GET, HEAD, OPTIONS, POST, PUT, PATCH or DELETE"`
	Path   string              `json:"path" jsonschema:"path relative to the 2.0 API, starting with /; may include query parameters; no absolute URLs"`
	Query  map[string][]string `json:"query,omitempty" jsonschema:"additional query parameters; each value is an array, preserving repeated parameters"`
	Body   any                 `json:"body,omitempty" jsonschema:"optional non-secret JSON body, at most 64 KiB; no body for GET, HEAD or OPTIONS"`
}

type APIResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        any    `json:"body,omitempty"`
	Text        string `json:"text,omitempty"`
}

// ValidateAPIRequest runs before confirmation, without contacting Bitbucket.
func (c *Client) ValidateAPIRequest(in APIRequest) error {
	_, _, err := c.prepareAPIRequest(in)
	return err
}

func (c *Client) prepareAPIRequest(in APIRequest) (*url.URL, []byte, error) {
	if c.allowedProjects != nil || c.allowedRepos != nil {
		return nil, nil, errors.New("API proxy is unavailable with repository or project restrictions")
	}
	switch in.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, nil, errors.New("unsupported method; use GET, HEAD, OPTIONS, POST, PUT, PATCH or DELETE")
	}
	if len(in.Path) > maxAPIRequestBytes {
		return nil, nil, errors.New("API path is too long")
	}
	u, err := url.Parse(in.Path)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || !strings.HasPrefix(in.Path, "/") || strings.HasPrefix(in.Path, "//") {
		return nil, nil, errors.New("path must be relative to the Bitbucket 2.0 API, without an authority or fragment")
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\%") || strings.ContainsFunc(part, unicode.IsControl) {
			return nil, nil, errors.New("API path contains an empty, dot, parent, double-encoded or control segment")
		}
	}
	if parts[0] == "2.0" {
		return nil, nil, errors.New("omit /2.0 from the API-relative path")
	}
	if parts[0] == "repositories" || parts[0] == "workspaces" || parts[0] == "teams" || parts[0] == "snippets" {
		if len(parts) < 2 || parts[1] != c.cfg.Workspace {
			return nil, nil, errors.New("API path must name the configured workspace")
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, nil, errors.New("invalid API query parameters")
	}
	for key, values := range in.Query {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	for key := range query {
		if sensitiveAPIField(key) || c.redactAPICredentials(key) != key {
			return nil, nil, errors.New("credentials must not be supplied in query parameters")
		}
		for _, value := range query[key] {
			if c.redactAPICredentials(value) != value {
				return nil, nil, errors.New("server credentials must not be supplied in query parameters")
			}
		}
	}
	u.RawQuery = query.Encode()
	if sensitiveAPIBody(in.Body) {
		return nil, nil, errors.New("secrets must not be supplied as tool arguments; use dedicated tools with browser secret entry")
	}
	var body []byte
	if in.Body != nil {
		if in.Method == http.MethodGet || in.Method == http.MethodHead || in.Method == http.MethodOptions {
			return nil, nil, errors.New("GET, HEAD and OPTIONS must not have a body")
		}
		body, err = json.Marshal(in.Body)
		if err != nil {
			return nil, nil, errors.New("body must be valid JSON")
		}
	}
	if len(body)+len(u.String()) > maxAPIRequestBytes {
		return nil, nil, errors.New("API request must be at most 64 KiB")
	}
	if c.redactAPICredentials(u.String()) != u.String() || c.redactAPICredentials(u.Path) != u.Path || c.redactAPICredentials(string(body)) != string(body) {
		return nil, nil, errors.New("server credentials must not be supplied in API paths, parameters or bodies")
	}
	return u, body, nil
}

// DoAPIRequest never follows redirects or pagination, and bounds both sides of the request.
func (c *Client) DoAPIRequest(ctx context.Context, in APIRequest) (APIResponse, error) {
	u, body, err := c.prepareAPIRequest(in)
	if err != nil {
		return APIResponse{}, err
	}
	if until, ok := ctx.Value(writeDeadlineKey{}).(time.Time); ok && !time.Now().Before(until) {
		return APIResponse{}, errors.New("the approval expired; nothing changed")
	}
	if err = ctx.Err(); err != nil {
		return APIResponse{}, err
	}
	rawURL := APIURL + u.String()
	req, err := http.NewRequestWithContext(ctx, in.Method, rawURL, bytes.NewReader(body))
	if err != nil {
		return APIResponse{}, errors.New("invalid Bitbucket API request")
	}
	c.basicAuth(req)
	req.Header.Set("Accept", "application/json, text/plain")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return APIResponse{}, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return APIResponse{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return APIResponse{}, context.DeadlineExceeded
		}
		return APIResponse{}, errors.New("bitbucket API request failed; transport details withheld")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		err := newAPIError(in.Method, u.Path, resp)
		err.Message = c.redactAPICredentials(err.Message)
		return APIResponse{}, err
	}
	out := APIResponse{Status: resp.StatusCode, ContentType: c.redactAPICredentials(resp.Header.Get("Content-Type"))}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes+1))
	if err != nil || len(data) > maxAPIResponseBytes {
		return APIResponse{}, fmt.Errorf("bitbucket returned HTTP %d but its response is unreadable or exceeds 40 KiB; reduce pagelen or fields for reads; do not repeat writes without checking their outcome", resp.StatusCode)
	}
	if len(data) == 0 {
		return out, nil
	}
	if json.Valid(data) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err = decoder.Decode(&out.Body); err != nil {
			return APIResponse{}, errors.New("invalid Bitbucket JSON response")
		}
		out.Body = c.redactAPIValue(out.Body)
	} else {
		if strings.Contains(strings.ToLower(out.ContentType), "json") {
			return APIResponse{}, fmt.Errorf("bitbucket returned HTTP %d with invalid JSON; response withheld; check the outcome before repeating writes", resp.StatusCode)
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return APIResponse{}, fmt.Errorf("bitbucket returned HTTP %d with a binary response; binary downloads are unsupported", resp.StatusCode)
		}
		out.Text = c.redactAPICredentials(string(data))
	}
	return out, nil
}

func sensitiveAPIField(name string) bool {
	name = strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(name))
	return strings.Contains(name, "token") || strings.Contains(name, "password") || strings.Contains(name, "secret") || strings.Contains(name, "privatekey") || name == "authorization" || name == "cookie" || name == "credentials"
}

func sensitiveAPIBody(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		if v["secured"] == true && v["value"] != nil {
			return true
		}
		for key, child := range v {
			if sensitiveAPIField(key) || sensitiveAPIBody(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if sensitiveAPIBody(child) {
				return true
			}
		}
	}
	return false
}

func (c *Client) redactAPIValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			// Partial-response fields can omit both the variable's key and its secured flag.
			if sensitiveAPIField(key) || key == "value" {
				child = "[REDACTED]"
			}
			out[c.redactAPICredentials(key)] = c.redactAPIValue(child)
		}
		return out
	case []any:
		for i := range v {
			v[i] = c.redactAPIValue(v[i])
		}
	case string:
		return c.redactAPICredentials(v)
	}
	return value
}

func (c *Client) redactAPICredentials(text string) string {
	if c.cfg.Token == "" {
		return text
	}
	basic := base64.StdEncoding.EncodeToString([]byte(c.cfg.Email + ":" + c.cfg.Token))
	return strings.NewReplacer(c.cfg.Token, "[REDACTED]", basic, "[REDACTED]").Replace(text)
}
