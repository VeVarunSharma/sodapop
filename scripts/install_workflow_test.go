package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeInstallWorkflowsExerciseRealPackages(t *testing.T) {
	files := map[string][]string{
		"install-tests.yml": {
			"go run ./scripts/installcheck",
			"npm run test:install",
			"portable-npm:",
			"homebrew:",
			"previous_version:",
			"name: verified-install-inputs",
			"name: verified-install-manifests",
			"cache: false",
			"Verify target-specific installation inputs",
			"--platform \"$SODAPOP_TARGET\"",
			"name: ${{ inputs.current_artifact_prefix }}${{ matrix.artifact }}",
			"pattern: ${{ inputs.current_artifact_prefix }}*",
			"pattern: ${{ inputs.previous_artifact_prefix }}*",
			"releasectl\" verify",
		},
		"native-candidates.yml": {
			"fixture.public-client",
			"for revision in 1 2",
			"name: Bundle the pinned runtime once",
			"SODAPOP_PREPARED_RUNTIME=1",
			"targeted_artifacts: true",
			"current_artifact_prefix: sodapop-current-",
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

func TestTargetedInstallDownloadsMatchChannelNeeds(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/install-tests.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, portable, found := strings.Cut(string(data), "  portable-npm:\n")
	if !found {
		t.Fatal("portable npm job is missing")
	}
	portable, homebrew, found := strings.Cut(portable, "  homebrew:\n")
	if !found {
		t.Fatal("Homebrew job is missing")
	}
	exact := "name: ${{ inputs.current_artifact_prefix }}${{ matrix.artifact }}"
	pattern := "pattern: ${{ inputs.current_artifact_prefix }}*"
	if !strings.Contains(portable, exact) || strings.Contains(portable, pattern) {
		t.Fatal("portable npm must download only its matrix target")
	}
	if !strings.Contains(homebrew, pattern) || !strings.Contains(homebrew, "merge-multiple: true") ||
		strings.Contains(homebrew, exact) {
		t.Fatal("Homebrew must download the complete verified release set")
	}
}

func TestCIAndReleaseExerciseTheSameWindowsPackages(t *testing.T) {
	const windowsTest = "go test -race ./internal/... ./scripts/installcheck ./scripts/releasectl ./scripts/windows"
	for _, workflow := range []string{"ci.yml", "release.yml"} {
		data, err := os.ReadFile(filepath.Join("../.github/workflows", workflow))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, required := range []string{
			"windows-2025",
			windowsTest,
			"go build ./cmd/sodapop",
		} {
			if !strings.Contains(text, required) {
				t.Errorf("%s is missing %q", workflow, required)
			}
		}
	}
}

func TestCISeparatesPortableQualityAndNPMDistribution(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"quality:",
		"- name: Vet\n        run: go vet ./...",
		"- name: Check formatting\n        shell: bash",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("CI is missing %q", required)
		}
	}
	index := strings.Index(text, "  distribution:")
	if index < 0 {
		t.Fatal("CI distribution job is missing")
	}
	distribution := text[index:]
	for _, required := range []string{
		"actions/setup-go@v5",
		"go-version-file: go.mod",
		"actions/setup-node@v4",
		"run: make npm-test",
	} {
		if !strings.Contains(distribution, required) {
			t.Errorf("CI distribution job is missing %q", required)
		}
	}
}

func TestPullRequestWorkflowsCancelSupersededRunsWithoutDuplicatingBranchCI(t *testing.T) {
	for file, required := range map[string][]string{
		"ci.yml": {
			"push:\n    branches: [main]",
			"pull_request:",
			"group: ci-${{ github.event.pull_request.number || github.ref }}",
			"cancel-in-progress: true",
		},
		"native-candidates.yml": {
			"pull_request:",
			"group: native-candidates-${{ github.event.pull_request.number || github.ref }}",
			"cancel-in-progress: true",
		},
	} {
		data, err := os.ReadFile("../.github/workflows/" + file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, value := range required {
			if !strings.Contains(text, value) {
				t.Errorf("%s is missing concurrency/trigger contract %q", file, value)
			}
		}
	}
}

func TestChannelPublicationRequiresAttestationsAndOwnerGates(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/publish-channels.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, requirement := range []string{
		"workflow_run:", "workflows: [Verify public native downloads]",
		"github.event.workflow_run.conclusion == 'success'",
		"github.event.workflow_run.event == 'release'", "workflow_dispatch:",
		"publish_npm: ${{ steps.release.outputs.publish_npm }}",
		"publish_homebrew: ${{ steps.release.outputs.publish_homebrew }}",
		"needs.prepare.outputs.publish_npm == 'true'",
		"needs.prepare.outputs.publish_homebrew == 'true'",
		"isImmutable == true", "isPrerelease == true",
		"gh release verify ", "gh release verify-asset ",
		"environment: npm-publish", "environment: homebrew-publish",
		"SODAPOP_STABLE_RELEASE_QUALIFIED", "SODAPOP_NPM_PUBLISH_ENABLED",
		"id-token: write", "npm@11.15.0", "node scripts/publish-npm.mjs",
		"permission-contents: write", "permission-pull-requests: write",
		"repositories: homebrew-sodapop", "gh pr create", "--registry-install",
		"needs: [prepare, npm]",
		"ref: ${{ github.event.repository.default_branch }}",
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

func TestChannelPublicationResolvesAutomaticAndManualSelections(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/publish-channels.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, step, found := strings.Cut(string(data), "      - name: Resolve an approved public release and channel selection\n")
	if !found {
		t.Fatal("release resolution step missing")
	}
	_, block, found := strings.Cut(step, "        run: |\n")
	if !found {
		t.Fatal("release resolution script missing")
	}
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		statement, ok := strings.CutPrefix(line, "          ")
		if !ok {
			break
		}
		lines = append(lines, statement)
	}

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	gh := `#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  "release view") printf '%s\n' "$FAKE_RELEASE_METADATA" ;;
  "release verify") printf 'verified\n' ;;
  *) printf 'unexpected gh invocation: %s\n' "$*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		event      string
		tag        string
		version    string
		npm        string
		homebrew   string
		metadata   string
		wantOutput string
		wantOK     bool
	}{
		{
			name: "automatic prerelease", event: "workflow_run", tag: "v1.2.3-rc.1",
			metadata:   `{"isDraft":false,"isImmutable":true,"isPrerelease":true}`,
			wantOutput: "version=1.2.3-rc.1\ntag=v1.2.3-rc.1\npublish_npm=true\npublish_homebrew=false\n",
			wantOK:     true,
		},
		{
			name: "automatic stable", event: "workflow_run", tag: "v1.2.3",
			metadata:   `{"isDraft":false,"isImmutable":true,"isPrerelease":false}`,
			wantOutput: "version=1.2.3\ntag=v1.2.3\npublish_npm=true\npublish_homebrew=true\n",
			wantOK:     true,
		},
		{
			name: "manual Homebrew recovery", event: "workflow_dispatch", version: "1.2.3",
			npm: "false", homebrew: "true",
			metadata:   `{"isDraft":false,"isImmutable":true,"isPrerelease":false}`,
			wantOutput: "version=1.2.3\ntag=v1.2.3\npublish_npm=false\npublish_homebrew=true\n",
			wantOK:     true,
		},
		{
			name: "invalid automatic tag", event: "workflow_run", tag: "main",
			metadata: `{"isDraft":false,"isImmutable":true,"isPrerelease":false}`,
			wantOK:   false,
		},
		{
			name: "mutable release", event: "workflow_run", tag: "v1.2.3",
			metadata: `{"isDraft":false,"isImmutable":false,"isPrerelease":false}`,
			wantOK:   false,
		},
		{
			name: "draft release", event: "workflow_run", tag: "v1.2.3",
			metadata: `{"isDraft":true,"isImmutable":true,"isPrerelease":false}`,
			wantOK:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outputFile := filepath.Join(t.TempDir(), "output")
			command := exec.Command("bash", "-euo", "pipefail", "-c", strings.Join(lines, "\n"))
			command.Env = []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"EVENT_NAME=" + tc.event,
				"AUTOMATIC_TAG=" + tc.tag,
				"INPUT_VERSION=" + tc.version,
				"INPUT_NPM=" + tc.npm,
				"INPUT_HOMEBREW=" + tc.homebrew,
				"FAKE_RELEASE_METADATA=" + tc.metadata,
				"GITHUB_REPOSITORY=VeVarunSharma/sodapop",
				"GITHUB_OUTPUT=" + outputFile,
			}
			output, err := command.CombinedOutput()
			if (err == nil) != tc.wantOK {
				t.Fatalf("release selection result: %s: %v", output, err)
			}
			if !tc.wantOK {
				return
			}
			got, err := os.ReadFile(outputFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.wantOutput {
				t.Fatalf("release outputs = %q, want %q", got, tc.wantOutput)
			}
		})
	}
}

func TestNPMBootstrapWorkflowIsOneTimeAndProtected(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/bootstrap-npm.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, requirement := range []string{
		"workflow_dispatch:", "environment: npm-publish",
		"SODAPOP_NPM_BOOTSTRAP_ENABLED", "NPM_BOOTSTRAP_TOKEN",
		"BOOTSTRAP @sodapop-sh", "isImmutable == true", "isPrerelease == true",
		"gh release verify ", "gh release verify-asset ", "go run ./scripts/releasectl verify",
		"registry-url: https://registry.npmjs.org", "npm@11.15.0",
		"node scripts/publish-npm.mjs", "preview",
	} {
		if !strings.Contains(text, requirement) {
			t.Errorf("npm bootstrap workflow is missing %q", requirement)
		}
	}
	for _, excluded := range []string{
		"pull_request_target:", "homebrew-publish", " latest", "--force", "--clobber",
	} {
		if strings.Contains(text, excluded) {
			t.Errorf("npm bootstrap workflow unexpectedly contains %q", excluded)
		}
	}
}
