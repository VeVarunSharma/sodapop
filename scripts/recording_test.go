package main

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var demoClips = []string{"overview", "commands", "themes", "diff"}

func recordingFixture(t *testing.T) (string, string) {
	t.Helper()
	root := scriptFixture(t)
	names := []string{"scripts/record-demos.sh", "docs/vhs/common.tape", "docs/vhs/vhs-version"}
	for _, clip := range demoClips {
		names = append(names, "docs/vhs/"+clip+".tape")
	}
	for _, state := range []string{"initial", "staged", "working"} {
		names = append(names, "docs/vhs/fixtures/main.go."+state)
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, name, string(data), 0600)
	}
	for _, tool := range []string{"ttyd", "ffmpeg"} {
		writeFixtureFile(t, root, "fake-bin/"+tool, "#!/bin/sh\nexit 0\n", 0700)
	}
	writeFixtureFile(t, root, "fake-bin/vhs", fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
root=%q
case "$1" in
  --version)
    if [[ -e "$root/wrong-version" ]]; then
      printf 'vhs version unsupported\n'
    else
      printf 'vhs version %%s\n' "$(tr -d '\n' < "$root/docs/vhs/vhs-version")"
    fi
    ;;
  validate)
    [[ -f "$2" && -f common.tape ]]
    command -v sodapop-recording >/dev/null
    ;;
  --quiet)
    clip="${2%%.tape}"
    printf '%%s\n' "$clip" >> "$root/recordings.log"
    env | sort > "$root/$clip.env"
    git -C soda-shop diff --cached -- main.go > "$root/$clip.staged"
    git -C soda-shop diff -- main.go > "$root/$clip.unstaged"
    printf 'GIF89a-fixture-%%s' "$clip" > "output/$clip.gif"
    if [[ -e "$root/fail-$clip" ]]; then
      printf 'fixture rendering failure\n' >&2
      exit 1
    fi
    if [[ ! -e "$root/omit-$clip-png" ]]; then
      printf '\211PNG\r\n\032\nfixture-%%s' "$clip" > "output/$clip.png"
    fi
    if [[ -e "$root/oversize-$clip" ]]; then
      dd if=/dev/zero of="output/$clip.gif" bs=1048576 count=4 2>/dev/null
    fi
    ;;
  *) printf 'unexpected VHS command\n' >&2; exit 1 ;;
esac
`, root), 0700)
	temp := filepath.Join(root, "recording temp")
	if err := os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	return root, temp
}

func assertNoRecordingStaging(t *testing.T, temp string) {
	t.Helper()
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("recording staging was not cleaned: %v, %v", entries, err)
	}
}

func TestMakeDemosRecordsIsolatedRealGitFixturesWithoutBundling(t *testing.T) {
	root, temp := recordingFixture(t)
	writeFixtureFile(t, root, "main.go", "USER_WORK_MUST_NOT_CHANGE", 0600)
	writeFixtureFile(t, root, ".sodapop.env", "SODAPOP_GITHUB_CLIENT_ID=fixture-client\n", 0600)
	output, err := runScriptFixture(t, root, []string{"make", "--no-print-directory", "demos"},
		"SODAPOP_DEMO_TMPDIR="+temp, "GOOS=windows", "GOARCH=386",
		"GITHUB_TOKEN=must-not-reach-recording", "GH_TOKEN=must-not-reach-recording",
		"COPILOT_GITHUB_TOKEN=must-not-reach-recording", "NO_COLOR=1",
		"GIT_DIR="+filepath.Join(root, "must-not-be-used.git"), "GIT_WORK_TREE="+root,
	)
	if err != nil || !strings.Contains(output, "Recorded 4 demo(s)") {
		t.Fatalf("recording failed: %s, %v", output, err)
	}
	assertFixtureLines(t, root, "recordings.log", demoClips)
	for _, clip := range demoClips {
		for _, extension := range []string{"gif", "png"} {
			data, err := os.ReadFile(filepath.Join(root, "docs/assets/demos", clip+"."+extension))
			if err != nil || !strings.Contains(string(data), "fixture-"+clip) {
				t.Fatalf("missing or incorrect %s.%s: %q, %v", clip, extension, data, err)
			}
		}
		data, err := os.ReadFile(filepath.Join(root, clip+".env"))
		if err != nil {
			t.Fatal(err)
		}
		environment := string(data)
		for _, forbidden := range []string{"GITHUB_TOKEN=", "GH_TOKEN=", "COPILOT_", "SODAPOP_", "NO_COLOR=", "GIT_DIR=", "GIT_WORK_TREE="} {
			if strings.Contains(environment, forbidden) {
				t.Fatalf("%s environment leaked %s", clip, forbidden)
			}
		}
		for _, required := range []string{"HOME=", "XDG_CONFIG_HOME=", "XDG_STATE_HOME=", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "USER=demo"} {
			if !strings.Contains(environment, required) {
				t.Fatalf("%s environment omitted %s", clip, required)
			}
		}
		for _, tc := range []struct {
			name, want, absent string
		}{
			{"staged", `+	fmt.Println("Pick a flavor: cola, lime, orange")`, `+	fmt.Println("Served chilled.")`},
			{"unstaged", `+	fmt.Println("Served chilled.")`, `+	fmt.Println("Pick a flavor: cola, lime, orange")`},
		} {
			diff, err := os.ReadFile(filepath.Join(root, clip+"."+tc.name))
			if err != nil || !strings.Contains(string(diff), tc.want) || strings.Contains(string(diff), tc.absent) {
				t.Fatalf("incorrect %s fixture: %s, %v", tc.name, diff, err)
			}
		}
	}
	build, err := os.ReadFile(filepath.Join(root, "go-build.log"))
	if err != nil || !strings.HasPrefix(string(build), "0\ndarwin\narm64\nbuild\n-trimpath\n-o\n") ||
		!strings.HasSuffix(string(build), "\n./scripts/recording\n") {
		t.Fatalf("recording did not build only the native launcher: %q, %v", build, err)
	}
	if _, err := os.Stat(filepath.Join(root, "go-tool.log")); !os.IsNotExist(err) {
		t.Fatalf("recording invoked the runtime bundler: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || string(data) != "USER_WORK_MUST_NOT_CHANGE" {
		t.Fatalf("recording changed the caller's worktree: %q, %v", data, err)
	}
	assertNoRecordingStaging(t, temp)
}

func TestDemosCanRegenerateOneClipWithoutReplacingOtherAssets(t *testing.T) {
	root, temp := recordingFixture(t)
	for _, clip := range demoClips {
		for _, extension := range []string{"gif", "png"} {
			writeFixtureFile(t, root, "docs/assets/demos/"+clip+"."+extension, "previous-"+clip, 0600)
		}
	}
	output, err := runScriptFixture(t, root, []string{"make", "--no-print-directory", "demos", "SODAPOP_DEMO=themes"}, "SODAPOP_DEMO_TMPDIR="+temp)
	if err != nil || !strings.Contains(output, "Recorded 1 demo(s)") {
		t.Fatalf("single recording failed: %s, %v", output, err)
	}
	assertFixtureLines(t, root, "recordings.log", []string{"themes"})
	for _, clip := range demoClips {
		for _, extension := range []string{"gif", "png"} {
			data, err := os.ReadFile(filepath.Join(root, "docs/assets/demos", clip+"."+extension))
			want := "previous-" + clip
			if clip == "themes" {
				want = "fixture-themes"
			}
			if err != nil || !strings.Contains(string(data), want) {
				t.Fatalf("incorrect asset replacement: %s.%s = %q, %v", clip, extension, data, err)
			}
		}
	}
	assertNoRecordingStaging(t, temp)
}

func TestRecordingFailureKeepsPublishedAssetsAndCleansStaging(t *testing.T) {
	for _, tc := range []struct {
		marker, want string
	}{
		{"fail-themes", "fixture rendering failure"},
		{"omit-themes-png", "did not produce themes.png"},
		{"oversize-themes", "exceeds the 3 MiB README budget"},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			root, temp := recordingFixture(t)
			writeFixtureFile(t, root, tc.marker, "", 0600)
			for _, clip := range demoClips {
				for _, extension := range []string{"gif", "png"} {
					writeFixtureFile(t, root, "docs/assets/demos/"+clip+"."+extension, "previous-"+clip, 0600)
				}
			}
			output, err := runScriptFixture(t, root, []string{"bash", "scripts/record-demos.sh"}, "SODAPOP_DEMO_TMPDIR="+temp)
			if err == nil || !strings.Contains(output, tc.want) {
				t.Fatalf("missing failure: %s, %v", output, err)
			}
			for _, clip := range demoClips {
				for _, extension := range []string{"gif", "png"} {
					data, err := os.ReadFile(filepath.Join(root, "docs/assets/demos", clip+"."+extension))
					if err != nil || string(data) != "previous-"+clip {
						t.Fatalf("failed batch published a partial result: %s.%s = %q, %v", clip, extension, data, err)
					}
				}
			}
			assertNoRecordingStaging(t, temp)
		})
	}
}

func TestDemosRejectInvalidInputsAndMissingToolsBeforeBuilding(t *testing.T) {
	for _, tc := range []struct {
		name, selector, missing, want string
		args                          []string
		wrongVersion                  bool
	}{
		{name: "unknown clip", selector: "../other", want: "SODAPOP_DEMO must be"},
		{name: "positional argument", args: []string{"overview"}, want: "positional arguments are not supported"},
		{name: "missing recorder", missing: "vhs", want: "Missing recording dependency: vhs"},
		{name: "missing encoder", missing: "ffmpeg", want: "Missing recording dependency: ffmpeg"},
		{name: "wrong VHS", wrongVersion: true, want: "Recordings require VHS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, temp := recordingFixture(t)
			env := []string{"SODAPOP_DEMO_TMPDIR=" + temp, "SODAPOP_DEMO=" + tc.selector}
			if tc.missing != "" {
				tools := filepath.Join(root, "only-tools")
				if err := os.Mkdir(tools, 0700); err != nil {
					t.Fatal(err)
				}
				for _, tool := range []string{"dirname", "git", "go", "vhs", "ttyd", "ffmpeg"} {
					if tool == tc.missing {
						continue
					}
					path := filepath.Join(root, "fake-bin", tool)
					if _, err := os.Stat(path); os.IsNotExist(err) {
						path, err = exec.LookPath(tool)
						if err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink(path, filepath.Join(tools, tool)); err != nil {
						t.Fatal(err)
					}
				}
				env = append(env, "PATH="+tools)
			}
			if tc.wrongVersion {
				writeFixtureFile(t, root, "wrong-version", "", 0600)
			}
			args := append([]string{"bash", "scripts/record-demos.sh"}, tc.args...)
			output, err := runScriptFixture(t, root, args, env...)
			if err == nil || !strings.Contains(output, tc.want) {
				t.Fatalf("missing preflight rejection: %s, %v", output, err)
			}
			for _, name := range []string{"go-build.log", "recordings.log", "docs/assets/demos"} {
				if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Fatalf("invalid recording created %s: %v", name, err)
				}
			}
			assertNoRecordingStaging(t, temp)
		})
	}
}

func TestDemosRejectsRelativeOrMissingTemporaryRoots(t *testing.T) {
	for _, directory := range []string{"relative", filepath.Join(t.TempDir(), "missing")} {
		root, temp := recordingFixture(t)
		output, err := runScriptFixture(t, root, []string{"bash", "scripts/record-demos.sh"}, "SODAPOP_DEMO_TMPDIR="+directory)
		if err == nil || !strings.Contains(output, "must be an existing absolute directory") {
			t.Fatalf("invalid temporary root was accepted: %s, %v", output, err)
		}
		if _, err := os.Stat(filepath.Join(root, "go-build.log")); !os.IsNotExist(err) {
			t.Fatalf("invalid temporary root started a build: %v", err)
		}
		assertNoRecordingStaging(t, temp)
	}
}

func TestDemoAssetsMatchREADMEAndCaptureGeometry(t *testing.T) {
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, clip := range demoClips {
		width, height := 1040, 650
		if clip == "overview" {
			width, height = 1440, 810
		}
		for _, extension := range []string{"gif", "png"} {
			path := "docs/assets/demos/" + clip + "." + extension
			if !strings.Contains(string(readme), path) {
				t.Errorf("README does not link %s", path)
			}
			file, err := os.Open("../" + path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if extension == "gif" && info.Size() > 3*1024*1024 {
				t.Errorf("%s exceeds the README GIF budget", path)
			}
			config, format, err := image.DecodeConfig(file)
			if err != nil || format != extension || config.Width != width || config.Height != height {
				t.Errorf("invalid %s: format=%s, config=%+v, err=%v", path, format, config, err)
			}
		}
	}
}

func TestDemoTapesUseBoundedWaitsAndProduceGIFsAndStills(t *testing.T) {
	for _, clip := range demoClips {
		data, err := os.ReadFile("../docs/vhs/" + clip + ".tape")
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		for _, required := range []string{"Source common.tape", "Output output/" + clip + ".gif", "Screenshot output/" + clip + ".png", "Require sodapop-recording"} {
			if !slices.Contains(lines, required) {
				t.Errorf("%s is missing %q", clip, required)
			}
		}
		for _, line := range lines {
			if strings.HasPrefix(line, "Wait") && !strings.HasPrefix(line, "Wait+Screen@") {
				t.Errorf("%s uses a wait without explicit screen scope/timeout: %s", clip, line)
			}
			if strings.Contains(line, "/login") || strings.Contains(line, "/autopilot") || strings.Contains(line, "/allow-all") || strings.Contains(line, "Publish") {
				t.Errorf("%s added authentication, unrestricted tools, or publishing", clip)
			}
		}
		if clip == "overview" || clip == "commands" {
			tape := string(data)
			if strings.Count(tape, "Shift+Tab") < 3 ||
				!strings.Contains(tape, "Wait+Screen@3s /plan>/") ||
				!strings.Contains(tape, "Wait+Screen@3s /auto>/") ||
				!strings.Contains(tape, "Wait+Screen@3s /chat>/") {
				t.Errorf("%s does not show the complete Chat/Plan/Autopilot cycle", clip)
			}
		}
		if clip == "commands" && !strings.Contains(string(data), `Type "vend"`) {
			t.Error("commands demo does not discover the vending machine")
		}
	}
}
