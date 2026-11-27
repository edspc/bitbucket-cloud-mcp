package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseRequiresSecurityChecks(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile("../../.github/workflows/" + name + ".yml")
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	job := func(workflow, name string) string {
		t.Helper()
		body := regexp.MustCompile(`(?m)^  ` + name + `:\n(    .*\n|\n)*`).FindString(workflow)
		if body == "" {
			t.Fatalf("workflow lacks the %s job", name)
		}
		return body
	}
	require := func(body string, lines ...string) {
		t.Helper()
		for _, line := range lines {
			if !strings.Contains("\n"+body, "\n"+line+"\n") {
				t.Errorf("workflow block lacks %q", line)
			}
		}
	}

	release, security := read("release"), read("security")
	const sha = "          ref: ${{ github.sha }}"
	require(job(release, "security"), "      contents: read", "    uses: ./.github/workflows/security.yml",
		"      SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}")
	publish := job(release, "release")
	require(publish, "    needs: security", "    if: github.ref_type == 'branch'", sha, "      - name: Push version commit and tag",
		"      - name: Build and publish with GoReleaser")
	require(security, "  workflow_call:\n    secrets:\n      SNYK_TOKEN:\n        required: true")
	snyk, osv := job(security, "snyk"), job(security, "osv-scanner")
	require(snyk, sha, `          test -n "$SNYK_TOKEN"`, "          make snyk")
	require(osv, sha, "      - name: Scan dependencies", "      - name: Scan the built server")
	if strings.Contains(snyk+osv, "continue-on-error:") {
		t.Error("security scans must fail the workflow on error")
	}
}
