package tools

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
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

func (t *tools) readWebhooks(ctx context.Context, repo string, in webhookReadInput, before *bitbucket.Repository) (repositoryActionResult, error) {
	out := repositoryActionResult{ContentTrust: "untrusted"}
	if in.UUID != "" {
		if in.Page != 0 || in.Limit != 0 {
			return out, errors.New("page and limit apply only to webhook listing")
		}
		id, err := webhookUUID(in.UUID)
		if err != nil {
			return out, err
		}
		hook, err := t.bb.Webhook(ctx, repo, id, before)
		if err != nil {
			return out, err
		}
		view := webhookView(hook)
		out.Webhook = &view
		return out, nil
	}
	page, limit, err := repositoryListPage(in.Page, in.Limit)
	if err != nil {
		return out, err
	}
	hooks, err := t.bb.ListWebhooks(ctx, repo, page, limit, before)
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

func (in webhookInput) validate() error {
	if in.Action == "list" || in.Action == "get" {
		if in.Settings != nil || in.SetSecret || in.RemoveSecret || (in.Action == "get") != (in.UUID != "") {
			return errors.New("get requires a uuid, list omits it, and neither accepts changes")
		}
		if in.Action == "get" {
			if in.Page != 0 || in.Limit != 0 {
				return errors.New("page and limit apply only to webhook listing")
			}
			_, err := webhookUUID(in.UUID)
			return err
		}
		_, _, err := repositoryListPage(in.Page, in.Limit)
		return err
	}
	if _, err := in.writeFields(); err != nil {
		return err
	}
	if in.Action != "create" {
		_, err := webhookUUID(in.UUID)
		return err
	}
	return nil
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

func (t *tools) planWebhook(ctx context.Context, repo string, beforeRepo bitbucket.Repository, args webhookInput) (repositoryOperation, error) {
	write, err := args.writeFields()
	if err != nil {
		return repositoryOperation{}, err
	}
	var id string
	var current *bitbucket.Webhook
	operation := repositoryOperation{}
	if args.Action != "create" {
		id, err = webhookUUID(args.UUID)
		if err != nil {
			return repositoryOperation{}, err
		}
		hook, err := t.bb.Webhook(ctx, repo, id, nil)
		if err != nil {
			return repositoryOperation{}, err
		}
		current = &hook
		view := webhookView(hook)
		operation.approval.CurrentWebhook = &view
		operation.hiddenURL = hook.URL
	}
	operation.apply = func(ctx context.Context, until time.Time, signingSecret string) (repositoryActionResult, error) {
		if current != nil {
			checkCtx, cancel := context.WithDeadline(ctx, until)
			defer cancel()
			hook, err := t.bb.Webhook(checkCtx, repo, id, nil)
			if err != nil {
				return repositoryActionResult{}, err
			}
			if !reflect.DeepEqual(*current, hook) {
				return repositoryActionResult{}, errors.New("webhook changed while awaiting approval; call the tool again")
			}
		}
		if args.SetSecret {
			write.Secret = &signingSecret
		}
		hook, err := t.bb.WriteWebhook(bitbucket.WithWriteDeadline(ctx, until), repo, beforeRepo, id, write)
		if err != nil {
			return repositoryActionResult{}, err
		}
		if write == nil {
			return repositoryActionResult{ContentTrust: "untrusted", DeletedWebhookUUID: id}, nil
		}
		view := webhookView(hook)
		return repositoryActionResult{ContentTrust: "untrusted", Webhook: &view}, nil
	}
	return operation, nil
}
