package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildInputsAreRejectedBeforeBundling(t *testing.T) {
	cases := []struct {
		name   string
		script string
		env    []string
		want   string
	}{
		{"unsupported target", "bundle.sh", []string{"SODAPOP_TARGET=freebsd/amd64"}, "Unsupported Sodapop target"},
		{"unsupported platform alias", "bundle.sh", []string{"SODAPOP_TARGET=macos/arm64"}, "Unsupported Sodapop target"},
		{"unsupported build target", "build.sh", []string{"SODAPOP_TARGET=windows/arm64"}, "Unsupported Sodapop target"},
		{"invalid version", "build.sh", []string{"SODAPOP_TARGET=darwin/arm64", "SODAPOP_VERSION=invalid version"}, "SODAPOP_VERSION"},
		{"invalid client ID", "build.sh", []string{"SODAPOP_TARGET=darwin/arm64", "SODAPOP_GITHUB_CLIENT_ID=not a client ID"}, "SODAPOP_GITHUB_CLIENT_ID"},
		{"invalid package target", "package.sh", []string{"SODAPOP_TARGET=linux/unsupported"}, "Unsupported Sodapop target"},
		{"invalid package version", "package.sh", []string{"SODAPOP_TARGET=linux/amd64", "SODAPOP_VERSION=../invalid"}, "Invalid SODAPOP_VERSION"},
		{"missing release client ID", "package.sh", []string{"SODAPOP_TARGET=linux/amd64", "SODAPOP_VERSION=v1.2.3"}, "requires SODAPOP_GITHUB_CLIENT_ID"},
		{"invalid release client ID", "package.sh", []string{"SODAPOP_TARGET=linux/amd64", "SODAPOP_VERSION=v1.2.3", "SODAPOP_GITHUB_CLIENT_ID=not a client ID"}, "must be a public client ID"},
		{"non-semantic package version", "package.sh", []string{"SODAPOP_TARGET=linux/amd64", "SODAPOP_VERSION=fixture", "SODAPOP_GITHUB_CLIENT_ID=fixture.public-client"}, "invalid release version"},
		{"noncanonical prerelease", "package.sh", []string{"SODAPOP_TARGET=linux/amd64", "SODAPOP_VERSION=1.2.3-01", "SODAPOP_GITHUB_CLIENT_ID=fixture.public-client"}, "invalid numeric prerelease"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := scriptFixture(t)
			outputPath := filepath.Join(root, "should-not-exist")
			output, err := runScriptFixture(t, root, []string{"bash", "scripts/" + tc.script},
				append(tc.env, "SODAPOP_OUTPUT="+outputPath)...)
			if err == nil || !strings.Contains(output, tc.want) {
				t.Fatalf("expected input rejection %q: %s, %v", tc.want, output, err)
			}
			for _, path := range []string{outputPath, filepath.Join(root, "go-tool.log"), filepath.Join(root, "go-build.log")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("invalid input invoked build tools or created output %s: %v", path, err)
				}
			}
		})
	}
}

func TestBuildReusesOnlyCompleteMatchingPreparedRuntime(t *testing.T) {
	writePreparedRuntime := func(t *testing.T, root string, complete bool) {
		t.Helper()
		source := `//go:build linux && amd64

package runtimebundle

import _ "embed"

//go:embed zcopilot_1.0.83_linux_amd64.zst
var cli []byte

//go:embed zcopilot_1.0.83_linux_amd64.license
var license []byte

func init() { _ = "fixture"; _ = struct{ Version string }{Version: "1.0.83"} }
`
		for _, name := range []string{"zcopilot_linux_amd64.go", "zcopilot_inprocess_linux_amd64.go"} {
			writeFixtureFile(t, root, "internal/runtimebundle/"+name, source, 0600)
		}
		writeFixtureFile(t, root, "internal/runtimebundle/zcopilot_1.0.83_linux_amd64.zst", "runtime", 0600)
		if complete {
			writeFixtureFile(t, root, "internal/runtimebundle/zcopilot_1.0.83_linux_amd64.license", "terms", 0600)
		}
	}

	t.Run("complete", func(t *testing.T) {
		root := scriptFixture(t)
		writePreparedRuntime(t, root, true)
		output, err := runScriptFixture(t, root, []string{"bash", "scripts/build.sh"},
			"SODAPOP_TARGET=linux/amd64", "SODAPOP_PREPARED_RUNTIME=1")
		if err != nil {
			t.Fatalf("prepared build failed: %s: %v", output, err)
		}
		if _, err := os.Stat(filepath.Join(root, "go-tool.log")); !os.IsNotExist(err) {
			t.Fatalf("prepared build invoked the bundler: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "go-build.log")); err != nil {
			t.Fatalf("prepared build did not compile: %v", err)
		}
	})

	t.Run("missing asset", func(t *testing.T) {
		root := scriptFixture(t)
		writePreparedRuntime(t, root, false)
		output, err := runScriptFixture(t, root, []string{"bash", "scripts/build.sh"},
			"SODAPOP_TARGET=linux/amd64", "SODAPOP_PREPARED_RUNTIME=1")
		if err == nil || !strings.Contains(output, "Prepared runtime asset is missing") {
			t.Fatalf("incomplete prepared runtime was accepted: %s: %v", output, err)
		}
		if _, err := os.Stat(filepath.Join(root, "go-build.log")); !os.IsNotExist(err) {
			t.Fatalf("invalid prepared runtime reached compilation: %v", err)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		root := scriptFixture(t)
		writePreparedRuntime(t, root, true)
		for _, name := range []string{"zcopilot_linux_amd64.go", "zcopilot_inprocess_linux_amd64.go"} {
			path := filepath.Join(root, "internal/runtimebundle", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), `Version: "1.0.83"`, `Version: "1.0.82"`)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		output, err := runScriptFixture(t, root, []string{"bash", "scripts/build.sh"},
			"SODAPOP_TARGET=linux/amd64", "SODAPOP_PREPARED_RUNTIME=1")
		if err == nil || !strings.Contains(output, "does not match 1.0.83") {
			t.Fatalf("wrong prepared runtime version was accepted: %s: %v", output, err)
		}
	})
}

func TestCoverageRatchet(t *testing.T) {
	cases := []struct {
		name     string
		baseline string
		coverage string
		wantErr  bool
		want     string
	}{
		{"above baseline", "80.0\n", "81.2", false, "current 81.2%, required 80.0%"},
		{"equal baseline", "80\n", "80.0", false, "current 80.0%, required 80.0%"},
		{"below baseline", "80.0\n", "79.9", true, "Coverage decreased: current 79.9%, required 80.0%"},
		{"malformed baseline", "eighty\n", "80.0", true, "must contain one numeric percentage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			baseline := filepath.Join(temp, "baseline.txt")
			if err := os.WriteFile(baseline, []byte(tc.baseline), 0600); err != nil {
				t.Fatal(err)
			}
			output, err := runCoverageScript(t, temp, baseline, tc.coverage, false, false)
			if (err != nil) != tc.wantErr || !strings.Contains(output, tc.want) {
				t.Fatalf("coverage result: %s, %v", output, err)
			}
			assertNoCoverageProfiles(t, temp)
		})
	}
}

func TestCoverageRatchetRejectsMissingBaselineAndMalformedToolOutput(t *testing.T) {
	temp := t.TempDir()
	missing := filepath.Join(temp, "missing.txt")
	output, err := runCoverageScript(t, temp, missing, "80.0", false, false)
	if err == nil || !strings.Contains(output, "Coverage baseline is missing") {
		t.Fatalf("missing baseline: %s, %v", output, err)
	}

	baseline := filepath.Join(temp, "baseline.txt")
	if err := os.WriteFile(baseline, []byte("80.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = runCoverageScript(t, temp, baseline, "80.0", true, false)
	if err == nil || !strings.Contains(output, "Could not parse total statement coverage") {
		t.Fatalf("malformed coverage output: %s, %v", output, err)
	}
	assertNoCoverageProfiles(t, temp)
}

func TestCoverageBaselineOnlyMovesUp(t *testing.T) {
	for _, tc := range []struct {
		name     string
		baseline string
		coverage string
		wantErr  bool
		want     string
		saved    string
	}{
		{"create", "", "72.4", false, "Created coverage baseline at 72.4%", "72.4\n"},
		{"increase", "72.4\n", "75.0", false, "Raised coverage baseline from 72.4% to 75.0%", "75.0\n"},
		{"equal", "72.4\n", "72.4", true, "can only increase", "72.4\n"},
		{"decrease", "72.4\n", "70.0", true, "can only increase", "72.4\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			baseline := filepath.Join(temp, "baseline.txt")
			if tc.baseline != "" {
				if err := os.WriteFile(baseline, []byte(tc.baseline), 0600); err != nil {
					t.Fatal(err)
				}
			}
			output, err := runCoverageScript(t, temp, baseline, tc.coverage, false, true)
			if (err != nil) != tc.wantErr || !strings.Contains(output, tc.want) {
				t.Fatalf("baseline update: %s, %v", output, err)
			}
			saved, readErr := os.ReadFile(baseline)
			if readErr != nil || string(saved) != tc.saved {
				t.Fatalf("saved baseline = %q, %v", saved, readErr)
			}
			assertNoCoverageProfiles(t, temp)
		})
	}
}

func TestPackageCoverageTargets(t *testing.T) {
	profile := `mode: atomic
github.com/VeVarunSharma/sodapop/internal/app/run.go:1.1,2.1 3 1
github.com/VeVarunSharma/sodapop/internal/app/run.go:3.1,4.1 1 0
github.com/VeVarunSharma/sodapop/internal/engine/sdk.go:1.1,2.1 9 1
github.com/VeVarunSharma/sodapop/internal/engine/sdk.go:3.1,4.1 1 0
github.com/VeVarunSharma/sodapop/internal/runtimebundle/runtime.go:1.1,2.1 9 1
github.com/VeVarunSharma/sodapop/internal/runtimebundle/runtime.go:3.1,4.1 1 0
github.com/VeVarunSharma/sodapop/internal/runtimebundle/zcopilot_darwin_arm64.go:1.1,2.1 100 0
`
	temp := t.TempDir()
	baseline := filepath.Join(temp, "baseline.txt")
	if err := os.WriteFile(baseline, []byte("80.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	targets := "internal/app 75\ninternal/engine 90\ninternal/runtimebundle 90\n"
	output, err := runCoverageScriptConfig(t, temp, baseline, "80.0", false, false, targets, profile)
	if err != nil || !strings.Contains(output, "internal/app is 75.0%") ||
		!strings.Contains(output, "internal/engine is 90.0%") ||
		!strings.Contains(output, "internal/runtimebundle is 90.0%") {
		t.Fatalf("package targets: %s, %v", output, err)
	}

	targets = "internal/app 75\ninternal/engine 90.1\n"
	output, err = runCoverageScriptConfig(t, temp, baseline, "80.0", false, false, targets, profile)
	if err == nil || !strings.Contains(output, "internal/engine is 90.0%, required 90.1%") {
		t.Fatalf("package failure: %s, %v", output, err)
	}
}

func runCoverageScript(t *testing.T, temp, baseline, coverage string, malformed, update bool) (string, error) {
	t.Helper()
	return runCoverageScriptConfig(t, temp, baseline, coverage, malformed, update, "", "")
}

func runCoverageScriptConfig(t *testing.T, temp, baseline, coverage string, malformed, update bool, targets, profile string) (string, error) {
	t.Helper()
	fakeGo := filepath.Join(temp, "fake-go")
	targetsFile := filepath.Join(temp, "targets.txt")
	if err := os.WriteFile(targetsFile, []byte(targets), 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  test)
    for arg in "$@"; do
      case "$arg" in
        -coverprofile=*) printf '%s' "${SODAPOP_FAKE_PROFILE:-mode: atomic
}" >"${arg#-coverprofile=}" ;;
      esac
    done
    ;;
  tool)
    if [[ "${SODAPOP_FAKE_MALFORMED:-}" == "1" ]]; then
      printf 'not coverage output\n'
    else
      printf 'total:\t(statements)\t%s%%\n' "$SODAPOP_FAKE_COVERAGE"
    fi
    ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(fakeGo, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"coverage.sh"}
	if update {
		args = append(args, "--update-baseline")
	}
	cmd := exec.Command("bash", args...)
	cmd.Env = append(os.Environ(),
		"SODAPOP_GO_COMMAND="+fakeGo,
		"SODAPOP_COVERAGE_BASELINE_FILE="+baseline,
		"SODAPOP_COVERAGE_TARGETS_FILE="+targetsFile,
		"SODAPOP_FAKE_COVERAGE="+coverage,
		"SODAPOP_FAKE_PROFILE="+profile,
		"TMPDIR="+temp,
	)
	if malformed {
		cmd.Env = append(cmd.Env, "SODAPOP_FAKE_MALFORMED=1")
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func assertNoCoverageProfiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "sodapop-coverage.") {
			t.Fatalf("temporary coverage profile was not removed: %s", entry.Name())
		}
	}
}
