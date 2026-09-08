package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/distribution"
)

func writeTestFile(t *testing.T, name, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func runCLI(args ...string) (string, error) {
	var output bytes.Buffer
	err := run(args, &output, &output)
	return output.String(), err
}

func cliRelease(t *testing.T, platform string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	stage := filepath.Join(t.TempDir(), "sodapop-1.2.3-"+strings.ReplaceAll(platform, "/", "-"))
	for _, name := range []string{distribution.BinaryName(platform), "LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSES/copilot-runtime.license", "LICENSES/dependency.license"} {
		writeTestFile(t, filepath.Join(stage, name), "fixture "+name, 0755)
	}
	if output, err := runCLI("archive", "--stage", stage, "--dir", dir, "--version", "1.2.3", "--platform", platform); err != nil {
		t.Fatalf("archive: %s: %v", output, err)
	}
	// Pin lookup is intentionally rooted at the caller's repository.
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "internal/runtimebundle/version.go"),
		"package runtimebundle\nconst(Version=\"1.0.83\";SDKVersion=\"1.0.13\")\n", 0644)
	writeTestFile(t, filepath.Join(repo, "go.mod"), "require github.com/github/copilot-sdk/go v1.0.13\n", 0644)
	t.Chdir(repo)
	output, err := runCLI("manifest", "--dir", dir, "--version", "1.2.3", "--commit", strings.Repeat("a", 40), "--platforms", platform)
	if err != nil || !strings.Contains(output, "Generated ") {
		t.Fatalf("manifest: %s: %v", output, err)
	}
	return dir, filepath.Join(dir, "sodapop-1.2.3-manifest.json")
}

func TestCLIEndToEnd(t *testing.T) {
	platforms := []string{"linux/amd64", "windows/amd64"}
	if runtime.GOOS == "windows" {
		platforms = []string{"windows/amd64"}
	}
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			dir, manifest := cliRelease(t, platform)
			for _, selector := range []string{"--platform", "--platforms"} {
				output, err := runCLI("verify", "--dir", dir, "--manifest", manifest, selector, platform)
				if err != nil || output != "" {
					t.Fatalf("verify: %s: %v", output, err)
				}
			}
			if _, err := runCLI("verify", "--dir", dir, "--manifest", manifest); err == nil {
				t.Fatal("default verifier accepted incomplete release")
			}
			extracted := filepath.Join(t.TempDir(), "result")
			if _, err := runCLI("extract", "--dir", dir, "--manifest", manifest, "--platform", platform, "--output", extracted); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(extracted, distribution.PackageName("1.2.3", platform), distribution.BinaryName(platform))
			if _, err := os.Stat(binary); err != nil {
				t.Fatal(err)
			}
			if _, err := runCLI("extract", "--dir", dir, "--manifest", manifest, "--platform", platform, "--output", extracted); err == nil {
				t.Fatal("extract replaced existing output")
			}
			m, err := distribution.ReadManifest(manifest)
			if err != nil {
				t.Fatal(err)
			}
			m.Artifacts[0].BinarySHA256 = strings.Repeat("0", 64)
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, manifest, string(data), 0644)
			if _, err := runCLI("verify", "--dir", dir, "--manifest", manifest, "--platform", platform); err == nil {
				t.Fatal("CLI accepted mismatched binary digest")
			}
		})
	}
}

func TestCLIRejectsInvalidArgumentsBeforeOutput(t *testing.T) {
	for _, args := range [][]string{
		nil, {"unknown"}, {"verify", "--bad"}, {"verify", "extra"},
		{"verify"}, {"verify", "--dir", "missing"},
		{"verify", "--dir", "missing", "--manifest", "missing"},
		{"verify", "--dir", "missing", "--platforms", ""},
		{"verify", "--dir", "missing", "--platforms", "linux/amd64,linux/amd64"},
		{"manifest", "--dir", "missing", "--version", "1.2.3", "--commit", strings.Repeat("a", 40)},
		{"manifest", "--dir", "missing", "--version", "bad"},
		{"archive", "--dir", "missing"},
		{"archive", "--dir", "missing", "--stage", "missing", "--platform", "linux/amd64", "--version", "1.2.3"},
		{"validate"}, {"validate", "--version", "1.2.3", "--platform", "linux/386"},
		{"validate", "--version", "1.2.3", "--platform", "linux/amd64,windows/amd64"},
	} {
		if _, err := runCLI(args...); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
	if output, err := runCLI("validate", "--version", "1.2.3", "--platform", "windows/amd64"); err != nil || output != "" {
		t.Fatalf("valid input rejected: %s: %v", output, err)
	}
}

func TestCLIFullReleaseDefaultSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable permission fixtures are covered on Unix runners; Windows covers the native ZIP path")
	}
	dir, firstManifest := cliRelease(t, "linux/amd64")
	if err := os.Remove(firstManifest); err != nil {
		t.Fatal(err)
	}
	for _, platform := range distribution.DefaultPlatforms() {
		if platform == "linux/amd64" {
			continue
		}
		stage := filepath.Join(t.TempDir(), distribution.PackageName("1.2.3", platform))
		for _, name := range []string{distribution.BinaryName(platform), "LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSES/copilot-runtime.license", "LICENSES/dependency.license"} {
			writeTestFile(t, filepath.Join(stage, name), "fixture "+platform+" "+name, 0755)
		}
		if _, err := runCLI("archive", "--stage", stage, "--dir", dir, "--version", "1.2.3", "--platform", platform); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runCLI("manifest", "--dir", dir, "--version", "1.2.3", "--commit", strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI("verify", "--dir", dir, "--manifest", firstManifest); err != nil {
		t.Fatal(err)
	}
}
