package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/commands"
)

const fixtureExecutable = "#!/usr/bin/env bash\nprintf \"fixture Sodapop launched\\n\"\n"

var (
	releaseHelperOnce sync.Once
	releaseHelperDir  string
	releaseHelperPath string
	releaseHelperErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if releaseHelperDir != "" {
		os.RemoveAll(releaseHelperDir)
	}
	os.Exit(code)
}

func releaseHelper(t *testing.T) string {
	t.Helper()
	releaseHelperOnce.Do(func() {
		releaseHelperDir, releaseHelperErr = os.MkdirTemp("", "sodapop-releasectl-test-")
		if releaseHelperErr != nil {
			return
		}
		releaseHelperPath = filepath.Join(releaseHelperDir, "releasectl")
		command := exec.Command("go", "build", "-o", releaseHelperPath, "./releasectl")
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			if name != "GOOS" && name != "GOARCH" && name != "GOFLAGS" &&
				name != "GOPROXY" && name != "GOSUMDB" && !strings.HasPrefix(name, "SODAPOP_") {
				command.Env = append(command.Env, entry)
			}
		}
		command.Env = append(command.Env, "GOPROXY=off", "GOSUMDB=off")
		output, err := command.CombinedOutput()
		if err != nil {
			releaseHelperErr = fmt.Errorf("build production release helper: %w: %s", err, output)
		}
	})
	if releaseHelperErr != nil {
		t.Fatal(releaseHelperErr)
	}
	return releaseHelperPath
}

func writeFixtureFile(t *testing.T, root, name, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func scriptFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"Makefile", "scripts/build.sh", "scripts/bundle.sh", "scripts/install.sh", "scripts/package.sh", "scripts/release-manifest.sh"} {
		data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, name, string(data), 0600)
	}
	writeFixtureFile(t, root, "internal/runtimebundle/version.go", "package runtimebundle\nconst (\nVersion = \"1.0.83\"\nSDKVersion = \"1.0.13\"\n)\n", 0600)
	writeFixtureFile(t, root, "go.mod", "module fixture\nrequire github.com/github/copilot-sdk/go v1.0.13\n", 0600)
	writeFixtureFile(t, root, "fake-bin/releasectl",
		"#!/usr/bin/env bash\nexec '"+strings.ReplaceAll(releaseHelper(t), "'", "'\\''")+"' \"$@\"\n", 0700)
	writeFixtureFile(t, root, "fake-bin/go", `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  env)
    case "$2" in
      GOHOSTOS) printf 'darwin\n' ;;
      GOHOSTARCH) printf 'arm64\n' ;;
      *) printf 'unexpected go env invocation\n' >&2; exit 1 ;;
    esac
    ;;
  tool)
    printf '%s\n' "${GOOS:-}" "${GOARCH:-}" "$@" > go-tool.log
    ;;
  build)
    printf '%s\n' "${CGO_ENABLED:-}" "${GOOS:-}" "${GOARCH:-}" "$@" > go-build.log
    output=
    while (( $# )); do
      if [[ "$1" == -o ]]; then
        shift
        output="$1"
      fi
      shift
    done
    if [[ -z "$output" ]]; then
      printf 'missing go build output\n' >&2
      exit 1
    fi
    printf '#!/usr/bin/env bash\nprintf "fixture Sodapop launched\\n"\n' > "$output"
    chmod 0755 "$output"
    ;;
  run)
    if [[ "${2:-}" == ./scripts/releasectl ]]; then
      shift 2
      exec "$(dirname "$0")/releasectl" "$@"
    fi
    if [[ $# != 3 || "$2" != ./scripts/notices.go ]]; then
      printf 'unexpected go run invocation\n' >&2
      exit 1
    fi
    mkdir -p "$3"
    printf 'fixture dependency license' > "$3/dependency.license"
    ;;
  test)
    printf '%s\n' "${SODAPOP_RUNTIME_SMOKE:-}" "${SODAPOP_LIVE_QUALIFY:-}" "${SODAPOP_LIVE_MODEL:-}" "$@" > go-test.log
    ;;
  *) printf 'unexpected go invocation\n' >&2; exit 1 ;;
esac
`, 0700)
	return root
}

func runScriptFixture(t *testing.T, root string, args []string, env ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "SODAPOP_") || slices.Contains([]string{
			"PATH", "HOME", "PWD", "OLDPWD", "BASH_ENV", "ENV", "MAKEFLAGS", "MFLAGS", "MAKELEVEL", "GNUMAKEFLAGS", "MAKEFILES",
		}, name) {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "HOME="+filepath.Join(root, "home"), "PWD="+root,
		"PATH="+filepath.Join(root, "fake-bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, env...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func assertFixtureLines(t *testing.T, root, name string, want []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"); !slices.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func TestBuildUsesSodapopNamespaceAndSupportedTargets(t *testing.T) {
	for _, target := range []string{"", "darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64", "windows/amd64"} {
		name := target
		if name == "" {
			name = "host defaults"
		}
		t.Run(name, func(t *testing.T) {
			root := scriptFixture(t)
			version, clientID, outputPath := "dev", "", "bin/sodapop"
			env := []string{"SODAPOP_TARGET=" + target, "GOOS=windows", "GOARCH=386"}
			if target == "" {
				target = "darwin/arm64"
			} else {
				version, clientID, outputPath = "v1.2.3-rc.1+fixture", "fixture.public-client_1", "custom bin/sodapop"
				if strings.HasPrefix(target, "windows/") {
					outputPath += ".exe"
				}
				env = append(env, "SODAPOP_VERSION="+version, "SODAPOP_GITHUB_CLIENT_ID="+clientID, "SODAPOP_OUTPUT="+outputPath)
			}
			output, err := runScriptFixture(t, root, []string{"bash", "scripts/build.sh"}, env...)
			if err != nil {
				t.Fatalf("build fixture failed: %s: %v", output, err)
			}
			assertFixtureLines(t, root, "go-tool.log", []string{
				"darwin", "arm64", "tool", "bundler", "--cli-version", "1.0.83", "--platform", target, "--output", "internal/runtimebundle",
			})
			goos, goarch, _ := strings.Cut(target, "/")
			assertFixtureLines(t, root, "go-build.log", []string{
				"0", goos, goarch, "build", "-trimpath", "-ldflags",
				"-s -w -X github.com/VeVarunSharma/sodapop/internal/app.Version=" + version +
					" -X github.com/VeVarunSharma/sodapop/internal/app.OAuthClientID=" + clientID,
				"-o", outputPath, "./cmd/sodapop",
			})
			if !strings.Contains(output, "Built "+outputPath+" for "+target) ||
				strings.Contains(output, "set SODAPOP_GITHUB_CLIENT_ID at launch or build time") != (clientID == "") {
				t.Fatalf("incorrect build guidance: %s", output)
			}
			info, err := os.Stat(filepath.Join(root, outputPath))
			if err != nil || info.Mode().Perm() != 0755 {
				t.Fatalf("missing executable output: %v, %v", info, err)
			}
		})
	}
}

func TestWindowsBuildDefaultsToExeOutput(t *testing.T) {
	root := scriptFixture(t)
	output, err := runScriptFixture(t, root, []string{"bash", "scripts/build.sh"},
		"SODAPOP_TARGET=windows/amd64", "SODAPOP_GITHUB_CLIENT_ID=fixture.public-client")
	if err != nil || !strings.Contains(output, "Built bin/sodapop.exe for windows/amd64") {
		t.Fatalf("Windows build fixture failed: %s: %v", output, err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "sodapop.exe")); err != nil {
		t.Fatalf("Windows build did not create the native executable name: %v", err)
	}
}

func TestMakeLoadsSodapopPublicClientConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fileID     string
		commandID  string
		wantClient string
	}{
		{"local file", "fixture.file-client", "", "fixture.file-client"},
		{"command override", "fixture.file-client", "fixture.command-client", "fixture.command-client"},
		{"exported client", "", "", "fixture.environment-client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scriptFixture(t)
			if tc.fileID != "" {
				writeFixtureFile(t, root, ".sodapop.env", "SODAPOP_GITHUB_CLIENT_ID="+tc.fileID+"\n", 0600)
			}
			for _, name := range []string{".env", ".sodapop.env.example", ".sodapop.env.local"} {
				writeFixtureFile(t, root, name, "SODAPOP_GITHUB_CLIENT_ID=must not be loaded\n", 0600)
			}
			args := []string{"make", "--no-print-directory", "build"}
			if tc.commandID != "" {
				args = append(args, "SODAPOP_GITHUB_CLIENT_ID="+tc.commandID)
			}
			output, err := runScriptFixture(t, root, args, "SODAPOP_GITHUB_CLIENT_ID=fixture.environment-client")
			if err != nil {
				t.Fatalf("make build fixture failed: %s: %v", output, err)
			}
			data, err := os.ReadFile(filepath.Join(root, "go-build.log"))
			if err != nil || !strings.Contains(string(data), " -X github.com/VeVarunSharma/sodapop/internal/app.OAuthClientID="+tc.wantClient+"\n") {
				t.Fatalf("Makefile did not export the selected public client ID: %q, %v", data, err)
			}
		})
	}
}

func TestMakeStartLaunchesConfiguredOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		goal   string
		output string
	}{
		{"default", "start", ""},
		{"relative basename", "start", "custom-sodapop"},
		{"relative directory", "start", "custom bin/sodapop"},
		{"absolute path and run alias", "run", "absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scriptFixture(t)
			outputPath := tc.output
			if outputPath == "absolute" {
				outputPath = filepath.Join(root, "absolute bin", "sodapop")
			}
			output, err := runScriptFixture(t, root,
				[]string{"make", "--no-print-directory", tc.goal, "SODAPOP_OUTPUT=" + outputPath})
			if err != nil || !strings.Contains(output, "fixture Sodapop launched\n") {
				t.Fatalf("make %s did not launch its built output: %s: %v", tc.goal, output, err)
			}
		})
	}
}

func TestMakeExportsRenamedRuntimeGates(t *testing.T) {
	for _, tc := range []struct {
		goal string
		want []string
	}{
		{"runtime-smoke", []string{"1", "", "fixture model", "test", "./internal/runtimebundle", "-run", "^TestBundledRuntimeSmoke$"}},
		{"qualify", []string{"", "1", "fixture model", "test", "-count=1", "-timeout=10m", "-run", "^TestLiveQualification$", "-v", "./internal/integration"}},
	} {
		t.Run(tc.goal, func(t *testing.T) {
			root := scriptFixture(t)
			output, err := runScriptFixture(t, root, []string{"make", "--no-print-directory", tc.goal, "SODAPOP_LIVE_MODEL=fixture model"})
			if err != nil {
				t.Fatalf("make %s fixture failed: %s: %v", tc.goal, output, err)
			}
			assertFixtureLines(t, root, "go-test.log", tc.want)
		})
	}
}

func TestInstallUsesSodapopExecutableAndPathGuidance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dir      string
		absolute bool
		onPath   bool
	}{
		{"default destination outside PATH", "", false, false},
		{"absolute custom paths on PATH", "install bin", true, true},
		{"relative custom paths outside PATH", "relative bin", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scriptFixture(t)
			source := "bin/sodapop"
			destination := filepath.Join(root, "home", ".local", "bin")
			var env []string
			if tc.dir != "" {
				source = "custom bin/sodapop"
				destination = filepath.Join(root, tc.dir)
				installDir, outputPath := tc.dir, source
				if tc.absolute {
					installDir, outputPath = destination, filepath.Join(root, source)
				}
				env = []string{
					"SODAPOP_OUTPUT=" + outputPath,
					"SODAPOP_INSTALL_DIR=" + installDir,
				}
			}
			if tc.onPath {
				env = append(env, "PATH="+destination+string(os.PathListSeparator)+filepath.Join(root, "fake-bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			writeFixtureFile(t, root, source, fixtureExecutable, 0755)
			for _, profile := range []string{".profile", ".bashrc", ".zshrc"} {
				writeFixtureFile(t, root, "home/"+profile, "keep shell configuration\n", 0600)
			}
			output, err := runScriptFixture(t, root, []string{"bash", "scripts/install.sh"}, env...)
			if err != nil {
				t.Fatalf("install fixture failed: %s: %v", output, err)
			}
			displayDestination := destination
			if tc.dir != "" && !tc.absolute {
				displayDestination = tc.dir
			}
			want := "Installed " + displayDestination + "/sodapop\n"
			if tc.onPath {
				want += "Run sodapop from your project directory.\n"
			} else {
				want += "Add " + displayDestination + " to PATH, then run sodapop from your project directory.\n"
			}
			if output != want {
				t.Errorf("installer guidance = %q, want %q", output, want)
			}
			installed := filepath.Join(destination, "sodapop")
			data, err := os.ReadFile(installed)
			if err != nil || string(data) != fixtureExecutable {
				t.Fatalf("installed executable changed: %q, %v", data, err)
			}
			info, err := os.Stat(installed)
			if err != nil || info.Mode().Perm() != 0755 {
				t.Fatalf("installed executable permissions: %v, %v", info, err)
			}
			for _, profile := range []string{".profile", ".bashrc", ".zshrc"} {
				assertFixtureLines(t, root, filepath.Join("home", profile), []string{"keep shell configuration"})
			}
		})
	}
}

func TestInstallRejectsMissingNonExecutableAndCrossTargetInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   os.FileMode
		target string
		want   string
	}{
		{"missing executable", 0, "", "Build the Sodapop executable before installing it."},
		{"non-executable file", 0600, "", "Build the Sodapop executable before installing it."},
		{"cross-target executable", 0755, "linux/amd64", "Refusing to install a cross-target binary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scriptFixture(t)
			if tc.mode != 0 {
				writeFixtureFile(t, root, "bin/sodapop", fixtureExecutable, tc.mode)
			}
			output, err := runScriptFixture(t, root, []string{"bash", "scripts/install.sh"}, "SODAPOP_TARGET="+tc.target)
			if err == nil || !strings.Contains(output, tc.want) {
				t.Fatalf("expected installer rejection: %s: %v", output, err)
			}
			if _, err := os.Stat(filepath.Join(root, "home", ".local", "bin")); !os.IsNotExist(err) {
				t.Fatalf("rejected install created its destination: %v", err)
			}
		})
	}
}

func TestREADMETracksCurrentCommandCatalog(t *testing.T) {
	for _, document := range []struct{ name, path string }{
		{"README", "../README.md"},
		{"Website", "../docs/commands.md"},
	} {
		t.Run(document.name, func(t *testing.T) {
			data, err := os.ReadFile(document.path)
			if err != nil {
				t.Fatal(err)
			}
			var got, want []string
			for _, line := range strings.Split(string(data), "\n") {
				if rest, ok := strings.CutPrefix(line, "| `/"); ok {
					end := strings.IndexAny(rest, " `")
					if end < 0 {
						t.Fatalf("malformed command row: %q", line)
					}
					got = append(got, rest[:end])
				}
			}
			for _, command := range commands.All() {
				want = append(want, command.Name)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("%s command catalog = %q, want %q", document.name, got, want)
			}
		})
	}
}
