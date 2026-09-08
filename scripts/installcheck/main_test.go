package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunRequiresExplicitInputs(t *testing.T) {
	for _, args := range [][]string{nil, {"--release-dir", "files"}, {"--unknown"}, {"--release-dir", "files", "--manifest", "m", "--tool", "t", "extra"}} {
		if err := run(args, &bytes.Buffer{}, &bytes.Buffer{}, nil); err == nil {
			t.Fatalf("accepted incomplete options %v", args)
		}
	}
}

func TestSmokeRejectsFailuresAndUnexpectedOutput(t *testing.T) {
	manifest := releaseManifest{Version: "1.2.3", SDK: "1.0.13", Runtime: "1.0.83"}
	for _, argument := range []string{"--version", "--help", "--check-runtime"} {
		for _, commandError := range []bool{false, true} {
			t.Run(argument+map[bool]string{true: "-error", false: "-output"}[commandError], func(t *testing.T) {
				runner := func(_ context.Context, _ string, args, _ []string) (string, error) {
					if args[0] == argument {
						if commandError {
							return "", errors.New("fixture command failed")
						}
						return "unexpected", nil
					}
					return smokeOutput(args[0]), nil
				}
				if err := smoke("sodapop", manifest, nil, runner); err == nil {
					t.Fatal("invalid installed command succeeded")
				}
			})
		}
	}
}

func smokeOutput(argument string) string {
	switch argument {
	case "--version":
		return "sodapop 1.2.3\nCopilot SDK 1.0.13 / runtime 1.0.83\n"
	case "--help":
		return "Usage: sodapop [options]\n"
	default:
		return "Copilot 1.0.83 (protocol 3)\n"
	}
}

func TestCheckVerifiesBeforeExecutingAndCleansItsOwnDirectory(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	content := []byte("native payload fixture for orchestration only")
	digest := sha256.Sum256(content)
	manifest := map[string]any{
		"version": "1.2.3", "copilot_sdk_version": "1.0.13", "copilot_runtime_version": "1.0.83",
		"artifacts": []map[string]string{{"platform": runtime.GOOS + "/" + runtime.GOARCH, "binary_sha256": hex.EncodeToString(digest[:])}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{true: "bad hash", false: "valid"}[corrupt], func(t *testing.T) {
			extraction := ""
			launches := 0
			runner := func(_ context.Context, executable string, args, environment []string) (string, error) {
				if executable == "releasectl" {
					extraction = args[len(args)-1]
					root := filepath.Join(extraction, "sodapop-1.2.3-"+runtime.GOOS+"-"+runtime.GOARCH)
					if err := os.MkdirAll(root, 0700); err != nil {
						return "", err
					}
					name := "sodapop"
					if runtime.GOOS == "windows" {
						name += ".exe"
					}
					payload := content
					if corrupt {
						payload = []byte("wrong bytes")
					}
					return "", os.WriteFile(filepath.Join(root, name), payload, 0755)
				}
				launches++
				for _, value := range environment {
					if strings.HasPrefix(value, "GH_TOKEN=") || strings.HasPrefix(value, "COPILOT_") {
						t.Fatalf("ambient credentials inherited: %s", value)
					}
				}
				return smokeOutput(args[0]), nil
			}
			err := check(settings{releaseDir: "archives", manifest: manifestPath, tool: "releasectl"}, runner)
			if (err != nil) != corrupt {
				t.Fatalf("check error = %v", err)
			}
			if corrupt && launches != 0 || !corrupt && launches != 8 {
				t.Fatalf("unexpected installed-command count %d", launches)
			}
			if _, err := os.Stat(filepath.Dir(extraction)); !os.IsNotExist(err) {
				t.Fatalf("test installation was not cleaned: %v", err)
			}
		})
	}
}

func TestCheckStopsOnExtractionFailure(t *testing.T) {
	calls := 0
	err := check(settings{tool: "releasectl"}, func(context.Context, string, []string, []string) (string, error) {
		calls++
		return "invalid archive", errors.New("verification failed")
	})
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "verify and extract") {
		t.Fatalf("extraction failure: calls=%d, err=%v", calls, err)
	}
}

func TestManifestAndFileChecksRejectInvalidInputs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "record")
	if _, err := readManifest(path); err == nil {
		t.Fatal("missing manifest accepted")
	}
	for _, content := range []string{"{", `{}`, `{"version":"../other","copilot_sdk_version":"1","copilot_runtime_version":"2"}`, `{"version":"1","copilot_sdk_version":"1","copilot_runtime_version":"2"} {}`} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readManifest(path); err == nil {
			t.Fatalf("invalid manifest accepted: %s", content)
		}
	}
	if err := checkHash(root, ""); err == nil {
		t.Fatal("directory accepted as a command")
	}
	if err := checkHash(filepath.Join(root, "missing"), ""); err == nil {
		t.Fatal("missing command accepted")
	}
	if err := copyCommand(path, root); err == nil {
		t.Fatal("copy replaced existing destination")
	}
}

func TestEnvironmentDoesNotRequireGoNodeOrCopilot(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		getenv := func(string) string { return `C:\Windows` }
		values, err := isolatedEnvironment("fixture-home", goos, getenv)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if strings.HasPrefix(value, "GH_") || strings.HasPrefix(value, "GITHUB_") || strings.HasPrefix(value, "COPILOT_") {
				t.Fatalf("unsafe environment entry %s", value)
			}
		}
	}
	if _, err := isolatedEnvironment("home", "windows", func(string) string { return "" }); err == nil {
		t.Fatal("missing Windows system directory accepted")
	}
}

func TestExecuteReportsNativeProcessErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := execute(ctx, "not-a-real-command", nil, nil); err == nil {
		t.Fatal("failed native process reported success")
	}
}
