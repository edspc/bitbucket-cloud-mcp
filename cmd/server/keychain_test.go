package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitKeychainMustBeAFile(t *testing.T) {
	for _, keychain := range []string{filepath.Join(t.TempDir(), "missing.keychain-db"), t.TempDir()} {
		lookup, err := configurationLookupForKeychain(keychain)
		if err == nil || lookup != nil || !strings.Contains(err.Error(), "cannot open macOS Keychain") {
			t.Fatalf("invalid Keychain path did not stop loading: %v", err)
		}
		if err = saveKeychain(testConfiguration(), keychain); err == nil || !strings.Contains(err.Error(), "cannot open macOS Keychain") {
			t.Fatalf("invalid Keychain path did not stop saving: %v", err)
		}
	}
}

func testConfiguration() map[string]string {
	return map[string]string{
		"BITBUCKET_WORKSPACE": "team", "BITBUCKET_EMAIL": "a@example.invalid", "BITBUCKET_API_TOKEN": "secret",
		"BITBUCKET_ALLOWED_REPOS": "[]", "BITBUCKET_CONFIRM_TOOLS": `["*"]`,
	}
}

func TestSavedConfiguration(t *testing.T) {
	values, err := validateConfiguration(testConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	lookup := mergedLookup(values, func(name string) (string, bool) {
		if name == "BITBUCKET_WORKSPACE" {
			return "override", true
		}
		return "", false
	})
	cfg, err := configFromLookup(lookup)
	if err != nil || cfg.Workspace != "override" || cfg.Token != "secret" {
		t.Fatalf("configuration resolution failed: %v", err)
	}
	policy, err := policyFromLookup(lookup)
	if err != nil || policy.Repositories == nil || len(policy.Repositories) != 0 || len(policy.ConfirmationTools) != 1 {
		t.Fatalf("saved policy: %+v, %v", policy, err)
	}
	lookup = mergedLookup(values, func(string) (string, bool) { return "", true })
	if _, err = configFromLookup(lookup); err == nil {
		t.Fatal("empty environment must override saved credentials")
	}
	if _, err = policyFromLookup(lookup); err == nil {
		t.Fatal("empty environment must not remove saved restrictions silently")
	}
}

func TestInvalidSavedConfiguration(t *testing.T) {
	for _, values := range []map[string]string{
		nil, {}, {"unknown": "value"},
		{"BITBUCKET_WORKSPACE": "team", "BITBUCKET_EMAIL": "email", "BITBUCKET_API_TOKEN": "secret", "BITBUCKET_ALLOWED_REPOS": "null"},
	} {
		if _, err := validateConfiguration(values); err == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
}

func TestKeychainCommandEscapesSecrets(t *testing.T) {
	command, err := keychainWriteCommand(map[string]string{"token": "a\"b\\c\nnext"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(command, "\n") != 1 || !strings.Contains(command, `\"token\"`) || !strings.HasPrefix(command, "add-generic-password -U ") {
		t.Fatal("unsafe security command encoding")
	}
}

func TestKeychainCommandSizeBoundary(t *testing.T) {
	for _, keychain := range []string{"", "/tmp/keychain with spaces.keychain-db"} {
		values := map[string]string{"BITBUCKET_API_TOKEN": ""}
		command, err := keychainWriteCommand(values, keychain)
		if err != nil {
			t.Fatal(err)
		}
		// The newline is read separately when all 4095 bytes of the buffer are occupied.
		remaining := 4096 - len(command)
		values["BITBUCKET_API_TOKEN"] = strings.Repeat("x", remaining)
		command, err = keychainWriteCommand(values, keychain)
		if err != nil || len(command) != 4096 {
			t.Fatalf("maximum command: bytes=%d, err=%v", len(command), err)
		}
		values["BITBUCKET_API_TOKEN"] += "x"
		command, err = keychainWriteCommand(values, keychain)
		if err == nil || command != "" {
			t.Fatal("accepted command that security would truncate")
		}
	}
	// Check escaped bytes, not the original input length or Unicode character count.
	for _, token := range []string{strings.Repeat(`"`, 1100), strings.Repeat("é", 2100)} {
		if _, err := keychainWriteCommand(map[string]string{"BITBUCKET_API_TOKEN": token}, ""); err == nil {
			t.Fatal("accepted oversized encoded command")
		}
	}
}

func TestKeychainPathCannotInjectCommands(t *testing.T) {
	for _, path := range []string{"a\nb", "a\rb", "a\x00b"} {
		if _, err := keychainWriteCommand(testConfiguration(), path); err == nil {
			t.Fatal("accepted unsafe Keychain path")
		}
	}
}

func TestAllSavedParameters(t *testing.T) {
	values := testConfiguration()
	values["BITBUCKET_ALLOWED_REPOS"] = `["repo"]`
	values["BITBUCKET_ALLOWED_PROJECTS"] = `["APP"]`
	values["BITBUCKET_ALLOWED_TOOLS"] = `["get_pipeline"]`
	values["BITBUCKET_ALLOWED_REFS"] = `["branch:main"]`
	values["BITBUCKET_ENABLE_API_PROXY"] = "true"
	values["BITBUCKET_LOCAL_BROWSER"] = "true"
	if _, err := validateConfiguration(values); err != nil {
		t.Fatal(err)
	}
	lookup := mergedLookup(values, func(string) (string, bool) { return "", false })
	policy, err := policyFromLookup(lookup)
	if err != nil || len(policy.Repositories) != 1 || len(policy.Projects) != 1 || len(policy.Tools) != 1 || len(policy.Refs) != 1 || len(policy.ConfirmationTools) != 1 || !policy.APIProxyEnabled || !policy.LocalBrowser {
		t.Fatalf("saved parameters not applied: %+v, %v", policy, err)
	}
}

func TestSavedAPIProxyEnvironmentOverride(t *testing.T) {
	values := testConfiguration()
	values["BITBUCKET_ENABLE_API_PROXY"] = "true"
	lookup := mergedLookup(values, func(name string) (string, bool) {
		return "false", name == "BITBUCKET_ENABLE_API_PROXY"
	})
	policy, err := policyFromLookup(lookup)
	if err != nil || policy.APIProxyEnabled {
		t.Fatalf("saved proxy overrode environment: %+v, %v", policy, err)
	}
	values["BITBUCKET_ENABLE_API_PROXY"] = "invalid"
	if _, err = validateConfiguration(values); err == nil {
		t.Fatal("invalid saved API proxy switch accepted")
	}
}

func TestLocalBrowserConfiguration(t *testing.T) {
	for _, value := range []string{"true", "false", "invalid", ""} {
		values := testConfiguration()
		values["BITBUCKET_LOCAL_BROWSER"] = value
		_, err := validateConfiguration(values)
		if (err != nil) != (value == "invalid" || value == "") {
			t.Fatalf("local browser boolean: %q, %v", value, err)
		}
	}
	values := testConfiguration()
	values["BITBUCKET_LOCAL_BROWSER"] = "true"
	policy, err := policyFromLookup(mergedLookup(values, func(name string) (string, bool) { return "false", name == "BITBUCKET_LOCAL_BROWSER" }))
	if err != nil || policy.LocalBrowser {
		t.Fatalf("environment must override saved browser mode: %+v, %v", policy, err)
	}
}
