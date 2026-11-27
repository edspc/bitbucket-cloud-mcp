package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type webhookReadInput struct {
	UUID  string `json:"uuid,omitempty" jsonschema:"webhook UUID; omit to list webhooks"`
	Page  int    `json:"page,omitempty" jsonschema:"list page from 1"`
	Limit int    `json:"limit,omitempty" jsonschema:"webhooks per page; 10 by default, at most 50"`
}

type webhookInput struct {
	webhookReadInput
	Action       string                     `json:"action" jsonschema:"list, get, create, update or delete"`
	Settings     *bitbucket.WebhookSettings `json:"settings,omitempty" jsonschema:"webhook URL, description, active flag and subscribed events"`
	SetSecret    bool                       `json:"set_secret,omitempty" jsonschema:"enter a new HMAC signing secret in the browser approval page"`
	RemoveSecret bool                       `json:"remove_secret,omitempty" jsonschema:"clear an existing signing secret"`
}

func webhookUUID(value string) (string, error) {
	id := strings.TrimPrefix(strings.TrimSuffix(value, "}"), "{")
	valid, _ := regexp.MatchString(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`, id)
	if !valid || (value != id && value != braced(id)) {
		return "", errors.New("webhook uuid must be a Bitbucket UUID")
	}
	return braced(id), nil
}

func webhookView(hook bitbucket.Webhook) bitbucket.Webhook {
	hook.URL = bitbucket.PublicWebhookURL(hook.URL)
	return hook
}

func (t *tools) readWebhooks(ctx context.Context, repo string, in webhookReadInput) (manageRepositoryResult, error) {
	out := manageRepositoryResult{ContentTrust: "untrusted"}
	if in.UUID != "" {
		if in.Page != 0 || in.Limit != 0 {
			return out, errors.New("page and limit apply only to webhook listing")
		}
		id, err := webhookUUID(in.UUID)
		if err != nil {
			return out, err
		}
		hook, err := t.bb.Webhook(ctx, repo, id)
		if err != nil {
			return out, err
		}
		view := webhookView(hook)
		out.Webhook = &view
		return out, nil
	}
	page, err := optionalPage(in.Page)
	if err != nil {
		return out, err
	}
	limit, err := optionalCount("limit", in.Limit, defaultListLimit, maxListLimit)
	if err != nil {
		return out, err
	}
	hooks, err := t.bb.ListWebhooks(ctx, repo, page, limit)
	if err != nil {
		return out, err
	}
	views := make([]bitbucket.Webhook, 0, len(hooks.Values))
	for _, hook := range hooks.Values {
		views = append(views, webhookView(hook))
	}
	out.Webhooks, out.NextPage, out.Total = &views, nextPage(page, hooks.More), hooks.Total
	return out, nil
}

func (in webhookInput) writeFields() (*bitbucket.WebhookWrite, error) {
	if in.SetSecret && in.RemoveSecret {
		return nil, errors.New("set_secret and remove_secret are mutually exclusive")
	}
	if in.Page != 0 || in.Limit != 0 {
		return nil, errors.New("page and limit apply only to webhook listing")
	}
	fields := bitbucket.WebhookWrite{}
	if in.Settings != nil {
		fields.WebhookSettings = *in.Settings
	}
	switch in.Action {
	case "create":
		if in.UUID != "" || in.RemoveSecret || fields.URL == nil || fields.Events == nil || fields.Description == nil || fields.Active == nil {
			return nil, errors.New("creating a webhook requires url, description, active and events, with no uuid or remove_secret")
		}
	case "update":
		if fields.WebhookSettings == (bitbucket.WebhookSettings{}) && !in.SetSecret && !in.RemoveSecret {
			return nil, errors.New("updating a webhook requires at least one change")
		}
	case "delete":
		if in.Settings != nil || in.SetSecret || in.RemoveSecret {
			return nil, errors.New("deleting a webhook takes only its uuid")
		}
		return nil, nil
	default:
		return nil, errors.New("webhook action must be list, get, create, update or delete")
	}
	if fields.URL != nil {
		u, err := url.Parse(*fields.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, errors.New("webhook url must be an absolute HTTP or HTTPS URL without credentials or a fragment")
		}
	}
	if fields.Events != nil {
		if len(*fields.Events) == 0 {
			return nil, errors.New("events must contain at least one Bitbucket event")
		}
		seen := make(map[string]bool, len(*fields.Events))
		for _, event := range *fields.Events {
			if event == "" || strings.TrimSpace(event) != event || seen[event] {
				return nil, errors.New("events must contain unique, nonempty Bitbucket event keys")
			}
			seen[event] = true
		}
	}
	if in.RemoveSecret {
		fields.Secret = new(string)
	}
	return &fields, nil
}

func (t *tools) manageWebhook(ctx context.Context, req *mcp.CallToolRequest, in manageRepositoryInput) (*mcp.CallToolResult, manageRepositoryResult, error) {
	args := *in.Webhook
	out := manageRepositoryResult{ContentTrust: "untrusted"}
	if args.Action == "list" || args.Action == "get" {
		if args.Settings != nil || args.SetSecret || args.RemoveSecret || (args.Action == "get") != (args.UUID != "") {
			return nil, out, errors.New("get requires a uuid, list omits it, and neither accepts changes")
		}
		out, err := t.readWebhooks(ctx, in.Repo, args.webhookReadInput)
		return nil, out, err
	}
	page, err := t.claimSecrets(req, in)
	if err != nil {
		return nil, out, err
	}
	defer page.close()
	write, err := args.writeFields()
	if err != nil {
		return nil, out, err
	}
	var id string
	if args.Action != "create" {
		id, err = webhookUUID(args.UUID)
		if err != nil {
			return nil, out, err
		}
	}
	repo, err := t.bb.RepositorySettings(ctx, in.Repo)
	if err != nil {
		return nil, out, err
	}
	var current *bitbucket.Webhook
	var displayed *bitbucket.Webhook
	if id != "" {
		hook, err := t.bb.Webhook(ctx, in.Repo, id)
		if err != nil {
			return nil, out, err
		}
		current = &hook
		view := webhookView(hook)
		displayed = &view
	}
	details := struct {
		manageRepositoryInput
		RepositoryUUID string             `json:"repository_uuid"`
		Current        *bitbucket.Webhook `json:"current_webhook,omitempty"`
	}{in, repo.UUID, displayed}
	var currentURL string
	if current != nil {
		currentURL = current.URL
	}
	data, err := json.MarshalIndent(details, "", "  ")
	if err != nil {
		return nil, out, err
	}
	if page == nil {
		info := secretPageInfo{
			Title: "Manage a webhook", Need: "Review the webhook and the requested change.",
			Outcome:   "Confirmation applies the displayed webhook change.",
			Arguments: string(data), webhookURL: currentURL,
		}
		if args.SetSecret {
			info.Fields = []secretField{{Name: "secret", Label: "Signing secret"}}
		}
		res, err := t.askSecrets(ctx, req, in, info)
		return res, out, err
	}
	if !page.automatic && (page.page.info.Arguments != string(data) || page.page.info.webhookURL != currentURL) {
		return nil, out, errors.New("webhook changed while awaiting approval; call the tool again")
	}
	if !accepted(req, page) {
		return nil, out, errors.New("webhook change was not approved")
	}
	values, err := t.waitSecrets(ctx, req, page)
	if err != nil {
		return nil, out, err
	}
	if args.SetSecret {
		secret := values["secret"]
		if secret == "" || len([]rune(secret)) > 128 {
			return nil, out, errors.New("webhook signing secret must contain 1 to 128 characters")
		}
		write.Secret = &secret
	}
	if current != nil {
		checkCtx, cancel := context.WithDeadline(ctx, page.page.info.Until)
		defer cancel()
		hook, err := t.bb.Webhook(checkCtx, in.Repo, id)
		if err != nil {
			return nil, out, err
		}
		if !reflect.DeepEqual(*current, hook) {
			return nil, out, errors.New("webhook changed while awaiting approval; call the tool again")
		}
	}
	if err = approvalLive(page); err != nil {
		return nil, out, err
	}
	hook, err := t.bb.WriteWebhook(bitbucket.WithWriteDeadline(ctx, page.page.info.Until), in.Repo, repo, id, write)
	if err != nil {
		return nil, out, err
	}
	if write == nil {
		out.DeletedWebhookUUID = id
	} else {
		view := webhookView(hook)
		out.Webhook = &view
	}
	return nil, out, nil
}
