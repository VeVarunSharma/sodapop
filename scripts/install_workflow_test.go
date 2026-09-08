package main

import (
	"os"
	"strings"
	"testing"
)

func TestNativeInstallWorkflowsExerciseRealPackages(t *testing.T) {
	files := map[string][]string{
		"install-tests.yml": {
			"go run ./scripts/installcheck",
			"npm run test:install",
			"previous_version:",
			"name: verified-install-inputs",
			"releasectl\" verify",
		},
		"native-candidates.yml": {
			"fixture.public-client",
			"for revision in 1 2",
			"bash scripts/package.sh",
			"uses: ./.github/workflows/install-tests.yml",
		},
		"public-downloads.yml": {
			"types: [published]",
			"bash scripts/test-public-download.sh",
			"persist-credentials: false",
			"contents: read",
		},
	}
	for file, required := range files {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile("../.github/workflows/" + file)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, value := range append(required, "macos-15", "macos-15-intel", "ubuntu-24.04", "ubuntu-24.04-arm", "windows-2025") {
				if !strings.Contains(text, value) {
					t.Errorf("missing native install contract %q", value)
				}
			}
			for _, unsafe := range []string{"continue-on-error: true", "npm publish", "SODAPOP_LIVE_QUALIFY: '1'"} {
				if strings.Contains(text, unsafe) {
					t.Errorf("native install tests unexpectedly contain %q", unsafe)
				}
			}
		})
	}
}

func TestChannelPublicationRequiresAttestationsAndOwnerGates(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/publish-channels.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, requirement := range []string{
		"workflow_dispatch:", "gh release verify ", "gh release verify-asset ",
		"environment: npm-publish", "environment: homebrew-publish",
		"SODAPOP_STABLE_RELEASE_QUALIFIED", "SODAPOP_NPM_PUBLISH_ENABLED",
		"id-token: write", "npm@11.15.0", "node scripts/publish-npm.mjs",
		"permission-contents: write", "permission-pull-requests: write",
		"repositories: homebrew-sodapop", "gh pr create", "--registry-install",
		"needs: [prepare, npm]",
	} {
		if !strings.Contains(text, requirement) {
			t.Errorf("publication workflow is missing %q", requirement)
		}
	}
	for _, excluded := range []string{"pull_request_target:", "--clobber", "--force", "gh pr merge", "NODE_AUTH_TOKEN:"} {
		if strings.Contains(text, excluded) {
			t.Errorf("publication workflow unexpectedly contains %q", excluded)
		}
	}
}
