package tools

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

type toolRule struct {
	name, repo string
	deny       bool
}

func parseToolRule(entry string) (toolRule, error) {
	name, deny := strings.CutPrefix(entry, "!")
	name, repo, scoped := strings.Cut(name, "@")
	if name == "" || name != "*" && strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-") != "" || scoped && !bitbucket.ValidRepositorySlug(repo) {
		return toolRule{}, errors.New("expected tool, !tool, tool@repo or !tool@repo; only a complete * wildcard is supported")
	}
	if name == "create_deployment" {
		name = "manage_deployment"
	}
	return toolRule{name: name, repo: repo, deny: deny}, nil
}

// ValidateToolRules rejects malformed restrictions before the server starts or saves them.
func ValidateToolRules(entries []string) error {
	for i, entry := range entries {
		if _, err := parseToolRule(entry); err != nil {
			return fmt.Errorf("tool rule %d: %w", i+1, err)
		}
	}
	return nil
}

func (p Policy) hasRepositoryToolRules() bool {
	for _, entry := range p.Tools {
		if strings.Contains(entry, "@") {
			return true
		}
	}
	return false
}

func (p Policy) checkRenameToolRules(from, to string) error {
	if strings.EqualFold(from, to) {
		return nil
	}
	source, destination := make(map[toolRule]bool), make(map[toolRule]bool)
	for _, entry := range p.Tools {
		rule, err := parseToolRule(entry)
		if err != nil {
			return fmt.Errorf("invalid server policy: %w", err)
		}
		repo := rule.repo
		rule.repo = ""
		switch {
		case strings.EqualFold(repo, from):
			source[rule] = true
		case strings.EqualFold(repo, to):
			destination[rule] = true
		}
	}
	if !maps.Equal(source, destination) {
		return errors.New("repository rename would change applicable tool rules under the server policy")
	}
	return nil
}

func (p Policy) checkTool(name string, repos []string) error {
	rules := make([]toolRule, 0, len(p.Tools))
	allowlist := false
	scoped := false
	for _, entry := range p.Tools {
		rule, err := parseToolRule(entry)
		if err != nil {
			return fmt.Errorf("invalid server policy: %w", err)
		}
		rules = append(rules, rule)
		allowlist = allowlist || !rule.deny
		scoped = scoped || rule.repo != "" && (rule.name == name || rule.name == "*")
	}
	if len(repos) == 0 {
		// A workspace search or operation cannot evade a rule by omitting its repository.
		if scoped {
			return errors.New("tool requires an explicit repository under the server policy")
		}
		repos = []string{""}
	}
	for _, repo := range repos {
		allowed := !allowlist
		for _, rule := range rules {
			if rule.name != name && rule.name != "*" || rule.repo != "" && !strings.EqualFold(rule.repo, repo) {
				continue
			}
			// Denials always win, independently of order and wildcard allowances.
			if rule.deny {
				return errors.New("tool is denied by the server policy")
			}
			allowed = true
		}
		if !allowed {
			return errors.New("tool is not allowed by the server policy")
		}
	}
	return nil
}
