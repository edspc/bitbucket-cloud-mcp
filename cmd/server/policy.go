package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/edspc/bitbucket-cloud-mcp/internal/tools"
)

func policyFromEnv() (tools.Policy, error) {
	return policyFromLookup(os.LookupEnv)
}

func policyFromLookup(lookup func(string) (string, bool)) (tools.Policy, error) {
	var policy tools.Policy
	if value, set := lookup("BITBUCKET_ENABLE_API_PROXY"); set {
		var err error
		policy.APIProxyEnabled, err = strconv.ParseBool(value)
		if err != nil {
			return tools.Policy{}, fmt.Errorf("BITBUCKET_ENABLE_API_PROXY must be a boolean")
		}
	}
	for _, field := range []struct {
		name string
		list *[]string
	}{
		{"BITBUCKET_ALLOWED_REPOS", &policy.Repositories},
		{"BITBUCKET_ALLOWED_PROJECTS", &policy.Projects},
		{"BITBUCKET_ALLOWED_TOOLS", &policy.Tools},
		{"BITBUCKET_ALLOWED_REFS", &policy.Refs},
		{"BITBUCKET_CONFIRM_TOOLS", &policy.ConfirmationTools},
	} {
		value, set := lookup(field.name)
		if !set {
			continue
		}
		if err := json.Unmarshal([]byte(value), field.list); err != nil || *field.list == nil {
			return tools.Policy{}, fmt.Errorf("%s must be a JSON array of exact names", field.name)
		}
		for _, entry := range *field.list {
			if entry == "" || strings.TrimSpace(entry) != entry {
				return tools.Policy{}, fmt.Errorf("%s must not contain empty names or surrounding whitespace", field.name)
			}
		}
	}
	return policy, nil
}
