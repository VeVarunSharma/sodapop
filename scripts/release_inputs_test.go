package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseVersionValidationPrecedesBuilds(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, step, found := strings.Cut(string(data), "      - name: Set release version\n")
	if !found {
		t.Fatal("release version step missing")
	}
	_, block, found := strings.Cut(step, "        run: |\n")
	if !found {
		t.Fatal("release version script missing")
	}
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		statement, ok := strings.CutPrefix(line, "          ")
		if !ok {
			break
		}
		lines = append(lines, statement)
	}
	for _, tc := range []struct {
		tag string
		ok  bool
	}{
		{"v1.2.3", true},
		{"v1.2.3-rc.1", true},
		{"1.2.3", false},
		{"v01.2.3", false},
		{"v1.2.3-01", false},
		{"v1.2.3-rc..1", false},
		{"v1.2.3+metadata", false},
		{"v1.2.3\nINJECTED=value", false},
		{"v1.2.3-" + strings.Repeat("a", 64), false},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			root := t.TempDir()
			envFile, outputFile := filepath.Join(root, "env"), filepath.Join(root, "output")
			command := exec.Command("bash", "-euo", "pipefail", "-c", strings.Join(lines, "\n"))
			command.Dir = root
			command.Env = []string{
				"PATH=" + os.Getenv("PATH"), "GITHUB_REF_NAME=" + tc.tag,
				"GITHUB_ENV=" + envFile, "GITHUB_OUTPUT=" + outputFile,
			}
			output, err := command.CombinedOutput()
			if (err == nil) != tc.ok {
				t.Fatalf("version gate: %s, %v", output, err)
			}
			if !tc.ok {
				if _, err := os.Stat(envFile); !os.IsNotExist(err) {
					t.Fatalf("invalid tag reached the build environment: %v", err)
				}
				return
			}
			value, err := os.ReadFile(envFile)
			if err != nil || string(value) != "SODAPOP_VERSION="+strings.TrimPrefix(tc.tag, "v")+"\n" {
				t.Fatalf("unexpected normalized version: %q, %v", value, err)
			}
		})
	}
}

func TestWindowsInstallerWorkflowRequiresExplicitNativePrerequisites(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/windows-installers.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range []string{
		"Assert-DisposableRunner",
		"accept_wix_terms:",
		"SODAPOP_WINDOWS_DISPOSABLE: '1'",
		"fromJSON(inputs.runner_labels)",
		"gh @('release', 'verify'",
		"Build-Msi.ps1",
		"-MsiLifecycle -AllowUnsignedCandidate",
		"Test-Channels.ps1",
		"Retain evidence, not unsigned installer downloads",
	} {
		if !strings.Contains(string(data), requirement) {
			t.Errorf("missing Windows installer prerequisite %q", requirement)
		}
	}
}
