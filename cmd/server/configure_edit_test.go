package main

import (
	"bytes"
	"io"
	"maps"
	"strings"
	"testing"
)

func TestConfigureEditsOnlySelectedSetting(t *testing.T) {
	for _, tc := range []struct {
		name, selection, value string
	}{
		{"BITBUCKET_WORKSPACE", "1", "other-team"},
		{"BITBUCKET_EMAIL", "2", "other@example.invalid"},
		{"BITBUCKET_API_TOKEN", "3", "rotated-private-token"},
		{"BITBUCKET_ALLOWED_REPOS", "4", `["repo-a","repo-b"]`},
		{"BITBUCKET_ALLOWED_PROJECTS", "5", `["APP","OPS"]`},
		{"BITBUCKET_ALLOWED_TOOLS", "6", `["*","!delete_variable@repo-a"]`},
		{"BITBUCKET_ALLOWED_REFS", "7", `["branch:release"]`},
		{"BITBUCKET_CONFIRM_TOOLS", "8", `["run_pipeline"]`},
		{"BITBUCKET_ENABLE_API_PROXY", "9", "true"},
		{"BITBUCKET_LOCAL_BROWSER", "10", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := testConfiguration()
			saved["BITBUCKET_ALLOWED_PROJECTS"] = `["EXISTING"]`
			snapshot := maps.Clone(saved)
			answers := strings.NewReader(tc.selection + "\n" + tc.value + "\n")
			var out bytes.Buffer
			var hidden []bool
			values, err := promptConfigurationEdit(&out, func(_ string, secret bool) (string, error) {
				hidden = append(hidden, secret)
				return readTerminalLine(answers)
			}, saved)
			want := maps.Clone(saved)
			want[tc.name] = tc.value
			if err != nil || !maps.Equal(values, want) || !maps.Equal(saved, snapshot) {
				t.Fatalf("single-field update changed other settings: %v", err)
			}
			if len(hidden) != 2 || hidden[0] || hidden[1] != (tc.name == "BITBUCKET_API_TOKEN") {
				t.Fatal("token input was not hidden")
			}
			if strings.Contains(out.String(), saved["BITBUCKET_API_TOKEN"]) || strings.Contains(out.String(), "rotated-private-token") {
				t.Fatal("editing configuration disclosed a token")
			}
		})
	}
}

func TestConfigureEditBlankPreservesAndClearResetsOptionalSetting(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		clear        bool
	}{
		{"BITBUCKET_API_TOKEN", "", false},
		{"BITBUCKET_ALLOWED_REPOS", "", false},
		{"BITBUCKET_ALLOWED_PROJECTS", "", false},
		{"BITBUCKET_ALLOWED_REPOS", "/clear", true},
		{"BITBUCKET_ALLOWED_PROJECTS", "/clear", true},
		{"BITBUCKET_ENABLE_API_PROXY", "/clear", true},
	} {
		t.Run(tc.name+tc.answer, func(t *testing.T) {
			saved := testConfiguration()
			answers := strings.NewReader(tc.name + "\n" + tc.answer + "\n")
			values, err := promptConfigurationEdit(io.Discard, func(string, bool) (string, error) {
				return readTerminalLine(answers)
			}, saved)
			want := maps.Clone(saved)
			if tc.clear {
				delete(want, tc.name)
			}
			if err != nil || !maps.Equal(values, want) {
				t.Fatalf("blank/clear changed other settings: %v", err)
			}
		})
	}
}

func TestConfigureEditRetriesInvalidSelectionAndValue(t *testing.T) {
	answers := strings.NewReader("invalid\n6\n[\"!delete_variable@\"]\n[\"*\",\"!delete_variable@repo-a\"]\n")
	var out bytes.Buffer
	values, err := promptConfigurationEdit(&out, func(string, bool) (string, error) {
		return readTerminalLine(answers)
	}, testConfiguration())
	if err != nil || values["BITBUCKET_ALLOWED_TOOLS"] != `["*","!delete_variable@repo-a"]` || !strings.Contains(out.String(), "Choose a listed setting") || !strings.Contains(out.String(), "tool rule") {
		t.Fatalf("invalid input did not retry: %v, %s", err, out.String())
	}
}

func TestConfigureEditCancellationNeverReturnsPartialSettings(t *testing.T) {
	for _, input := range []string{"", "0\n", "3\n", "4\n[invalid]\n"} {
		saved := testConfiguration()
		snapshot := maps.Clone(saved)
		answers := strings.NewReader(input)
		values, err := promptConfigurationEdit(io.Discard, func(string, bool) (string, error) {
			return readTerminalLine(answers)
		}, saved)
		if err == nil || values != nil || !maps.Equal(saved, snapshot) {
			t.Fatal("cancelled edit returned a partial configuration or changed saved settings")
		}
	}
}

func TestConfigureFullSetupFromMissingOrExplicitAll(t *testing.T) {
	for _, edit := range []bool{false, true} {
		saved := map[string]string{}
		input := "team\nemail\nnew-token\n\n\n\n\n\n\n\n"
		if edit {
			saved = testConfiguration()
			input = "all\n" + input
		}
		answers := strings.NewReader(input)
		values, err := promptConfigurationEdit(io.Discard, func(string, bool) (string, error) {
			return readTerminalLine(answers)
		}, saved)
		want := map[string]string{"BITBUCKET_WORKSPACE": "team", "BITBUCKET_EMAIL": "email", "BITBUCKET_API_TOKEN": "new-token"}
		if err != nil || !maps.Equal(values, want) {
			t.Fatalf("full setup: %v", err)
		}
	}
}
