package main

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func securityForTest(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "/usr/bin/security", args...).CombinedOutput(); err != nil {
		t.Fatalf("test Keychain command: %v\n%s", err, output)
	}
}

func temporaryKeychain(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "isolated test.keychain-db")
	securityForTest(t, "create-keychain", "-p", "test-only-password", path)
	t.Cleanup(func() {
		// t.Context is cancelled before cleanup; give deletion its own deadline.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, "/usr/bin/security", "delete-keychain", path).CombinedOutput(); err != nil {
			t.Errorf("delete temporary Keychain: %v\n%s", err, output)
		}
	})
	securityForTest(t, "set-keychain-settings", "-lut", "3600", path)
	securityForTest(t, "unlock-keychain", "-p", "test-only-password", path)
	return path
}

func clearConfigurationEnv(t *testing.T) {
	t.Helper()
	clearPolicyEnv(t)
	for _, name := range []string{"BITBUCKET_WORKSPACE", "BITBUCKET_EMAIL", "BITBUCKET_API_TOKEN"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKeychainIntegrationRoundTrip(t *testing.T) {
	clearConfigurationEnv(t)
	keychain := temporaryKeychain(t)
	values := testConfiguration()
	values["BITBUCKET_API_TOKEN"] = "fake-token-\"quoted\"-\\slash\nnext"
	values["BITBUCKET_ALLOWED_PROJECTS"] = `["APP"]`
	values["BITBUCKET_ALLOWED_TOOLS"] = `["get_pipeline"]`
	values["BITBUCKET_ALLOWED_REFS"] = `["branch:main"]`
	values["BITBUCKET_ENABLE_API_PROXY"] = "true"
	values["BITBUCKET_LOCAL_BROWSER"] = "true"
	if err := saveKeychain(values, keychain); err != nil {
		t.Fatal(err)
	}
	saved, err := loadKeychain(keychain)
	if err != nil || !maps.Equal(saved, values) {
		t.Fatal("Keychain round trip changed the configuration")
	}
	lookup, err := configurationLookupForKeychain(keychain)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := configFromLookup(lookup)
	if err != nil || cfg.Token != values["BITBUCKET_API_TOKEN"] || cfg.Workspace != "team" {
		t.Fatal("saved credentials were not loaded")
	}
	policy, err := policyFromLookup(lookup)
	if err != nil || policy.Repositories == nil || len(policy.Repositories) != 0 || len(policy.Projects) != 1 || len(policy.Tools) != 1 || len(policy.Refs) != 1 || len(policy.ConfirmationTools) != 1 || !policy.APIProxyEnabled || !policy.LocalBrowser {
		t.Fatalf("saved policy not loaded: %+v, %v", policy, err)
	}
	t.Setenv("BITBUCKET_WORKSPACE", "override")
	lookup, err = configurationLookupForKeychain(keychain)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = configFromLookup(lookup)
	if err != nil || cfg.Workspace != "override" {
		t.Fatal("environment did not override saved configuration")
	}
	values["BITBUCKET_WORKSPACE"] = "replacement"
	if err = saveKeychain(values, keychain); err != nil {
		t.Fatal(err)
	}
	saved, err = loadKeychain(keychain)
	if err != nil || !maps.Equal(saved, values) {
		t.Fatal("existing Keychain item was not replaced")
	}
}

func TestKeychainIntegrationOversizePreservesSettings(t *testing.T) {
	keychain := temporaryKeychain(t)
	original := testConfiguration()
	if err := saveKeychain(original, keychain); err != nil {
		t.Fatal(err)
	}
	oversized := maps.Clone(original)
	oversized["BITBUCKET_API_TOKEN"] = strings.Repeat("x", 4096)
	if err := saveKeychain(oversized, keychain); err == nil {
		t.Fatal("oversized configuration was accepted")
	}
	saved, err := loadKeychain(keychain)
	if err != nil || !maps.Equal(saved, original) {
		t.Fatal("oversized configuration changed existing settings")
	}
}

func TestKeychainIntegrationMissingItem(t *testing.T) {
	clearConfigurationEnv(t)
	keychain := temporaryKeychain(t)
	t.Setenv("BITBUCKET_WORKSPACE", "environment")
	t.Setenv("BITBUCKET_EMAIL", "email")
	t.Setenv("BITBUCKET_API_TOKEN", "fake-token")
	lookup, err := configurationLookupForKeychain(keychain)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := configFromLookup(lookup)
	if err != nil || cfg.Workspace != "environment" {
		t.Fatal("missing item did not fall back to environment")
	}
}

func TestKeychainIntegrationReadFailureStopsStartup(t *testing.T) {
	clearConfigurationEnv(t)
	t.Setenv("BITBUCKET_WORKSPACE", "environment")
	t.Setenv("BITBUCKET_EMAIL", "email")
	t.Setenv("BITBUCKET_API_TOKEN", "fake-token")
	lookup, err := configurationLookupForKeychain(filepath.Join(t.TempDir(), "missing.keychain-db"))
	if err == nil || lookup != nil {
		t.Fatal("Keychain read failure must not fall back to unrestricted environment configuration")
	}
}

func TestKeychainIntegrationInvalidJSONStopsStartup(t *testing.T) {
	keychain := temporaryKeychain(t)
	securityForTest(t, "add-generic-password", "-s", "bitbucket-cloud-mcp", "-a", "configuration", "-w", "{", keychain)
	lookup, err := configurationLookupForKeychain(keychain)
	if err == nil || lookup != nil {
		t.Fatal("invalid Keychain JSON must stop startup")
	}
}
