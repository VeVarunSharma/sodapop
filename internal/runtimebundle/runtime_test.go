package runtimebundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

func TestMissingBundleDoesNotFallBackToPATH(t *testing.T) {
	t.Setenv("COPILOT_CLI_PATH", "/some/existing/copilot")
	_, err := resolve(func() string { return "" }, "darwin", "arm64")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestRuntimePairRequired(t *testing.T) {
	dir := t.TempDir()
	install := func() string { return filepath.Join(dir, "copilot") }
	goos := runtime.GOOS
	if goos != "darwin" && goos != "linux" && goos != "windows" {
		goos = "linux"
	}
	executableName := runtimeExecutableName(goos)
	if _, err := resolve(install, goos, "amd64"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing runtime pair accepted")
	}
	executable := filepath.Join(dir, executableName)
	if err := os.WriteFile(executable, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(install, goos, "amd64"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing runtime.node accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.node"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := resolve(install, goos, "amd64")
	if err != nil || path != executable {
		t.Fatalf("valid pair: %s, %v", path, err)
	}
	if goos != "windows" {
		if err := os.Chmod(executable, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolve(install, goos, "amd64"); !errors.Is(err, ErrUnavailable) {
			t.Fatal("non-executable runtime accepted")
		}
		if err := os.Chmod(executable, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(executable, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(install, goos, "amd64"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("empty runtime accepted")
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(executable, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(install, goos, "amd64"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("runtime directory accepted")
	}
}

func TestUnsupportedPlatformDoesNotExtract(t *testing.T) {
	_, err := resolve(func() string {
		t.Fatal("unsupported platform extracted runtime")
		return ""
	}, "windows", "arm64")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unsupported platform: %v", err)
	}
}

func TestWindowsRuntimeUsesNativeExecutableName(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "copilot-runtime.exe")
	if err := os.WriteFile(executable, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.node"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := resolve(func() string { return filepath.Join(dir, "copilot.exe") }, "windows", "amd64")
	if err != nil || path != executable {
		t.Fatalf("Windows runtime path = %q, %v", path, err)
	}
}

func TestEnvironmentDropsAmbientIdentityAndRuntimeOverrides(t *testing.T) {
	got := cleanEnvironment([]string{
		"HOME=/home/sodapop", "PATH=/bin", "GH_TOKEN=secret", "GITHUB_TOKEN=other",
		"COPILOT_HOME=/another/app", "COPILOT_CLI_PATH=/other/copilot",
		"COPILOT_GITHUB_TOKEN=another", "COPILOT_SDK_DEFAULT_CONNECTION=inprocess",
		"GH_HOST=other.example", "PROJECT_SETTING=1",
	})
	want := []string{"HOME=/home/sodapop", "PATH=/bin", "PROJECT_SETTING=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected runtime environment: %v", got)
	}
}

func TestEnvironmentReadsCurrentProcess(t *testing.T) {
	t.Setenv("SODAPOP_RUNTIME_TEST_VALUE", "present")
	t.Setenv("GH_TOKEN", "secret")
	values := Environment()
	if !containsEnvironment(values, "SODAPOP_RUNTIME_TEST_VALUE=present") || containsEnvironment(values, "GH_TOKEN=secret") {
		t.Fatalf("unexpected environment: %v", values)
	}
}

func TestEnvironmentUsesPlatformVariableNameSemantics(t *testing.T) {
	input := []string{
		"Path=C:\\Windows\\System32", "=C:=C:\\project",
		"gh_token=fixture", "Github_Token=fixture", "gH_hOsT=example.invalid",
		"Copilot_Home=C:\\ambient", "copilot_cli_path=C:\\other\\copilot.exe",
		"COPILOT_GITHUB_TOKEN=fixture", "PROJECT_VALUE=one=two",
	}
	for _, goos := range []string{"windows", "darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			want := []string{
				input[0], input[1], input[2], input[3],
				input[4], input[5], input[6], input[8],
			}
			if goos == "windows" {
				want = []string{input[0], input[1], input[8]}
			}
			if got := cleanEnvironmentForPlatform(input, goos); !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected %s environment: %v", goos, got)
			}
		})
	}
}

func TestCheckUsesIsolatedRuntimeClient(t *testing.T) {
	temp := t.TempDir()
	home := filepath.Join(temp, "runtime-home")
	client := &fakeCheckClient{status: &copilot.GetStatusResponse{Version: "v" + Version, ProtocolVersion: 7}}
	var options *copilot.ClientOptions
	removed := ""
	deps := checkDependencies{
		path:      func() (string, error) { return "/bundle/copilot-runtime", nil },
		mkdirTemp: func(string, string) (string, error) { return home, nil },
		removeAll: func(path string) error {
			removed = path
			return nil
		},
		environment: func() []string { return []string{"PATH=/bin"} },
		newClient: func(got *copilot.ClientOptions) checkClient {
			options = got
			return client
		},
	}
	result, err := check(context.Background(), deps)
	if err != nil || result != "Copilot v"+Version+" (protocol 7)" {
		t.Fatalf("check result = %q, %v", result, err)
	}
	connection, ok := options.Connection.(copilot.StdioConnection)
	if !ok || connection.Path != "/bundle/copilot-runtime" || options.BaseDirectory != home ||
		options.WorkingDirectory != home || options.Mode != copilot.ModeEmpty ||
		options.UseLoggedInUser == nil || *options.UseLoggedInUser || !reflect.DeepEqual(options.Env, []string{"PATH=/bin"}) ||
		options.LogLevel != "none" || removed != home || client.starts != 1 || client.stops != 1 {
		t.Fatalf("unsafe runtime check: options=%+v client=%+v removed=%q", options, client, removed)
	}
}

func TestCheckReportsBoundaryFailuresAndAlwaysCleansUp(t *testing.T) {
	pathErr := errors.New("path failed")
	if _, err := check(context.Background(), checkDependencies{path: func() (string, error) { return "", pathErr }}); !errors.Is(err, pathErr) {
		t.Fatalf("path error = %v", err)
	}

	mkdirErr := errors.New("mkdir failed")
	deps := testCheckDependencies(&fakeCheckClient{})
	deps.mkdirTemp = func(string, string) (string, error) { return "", mkdirErr }
	if _, err := check(context.Background(), deps); !errors.Is(err, mkdirErr) || !strings.Contains(err.Error(), "create runtime check directory") {
		t.Fatalf("mkdir error = %v", err)
	}

	for _, tc := range []struct {
		name   string
		client *fakeCheckClient
		remove error
		want   string
	}{
		{"start", &fakeCheckClient{startErr: errors.New("start failed")}, nil, "start bundled runtime"},
		{"status", &fakeCheckClient{statusErr: errors.New("status failed")}, nil, "query bundled runtime"},
		{"missing status", &fakeCheckClient{}, nil, "query bundled runtime"},
		{"version", &fakeCheckClient{status: &copilot.GetStatusResponse{Version: "wrong"}}, nil, "version mismatch"},
		{"stop", &fakeCheckClient{status: &copilot.GetStatusResponse{Version: Version}, stopErr: errors.New("stop failed")}, nil, "stop failed"},
		{"cleanup", &fakeCheckClient{status: &copilot.GetStatusResponse{Version: Version}}, errors.New("cleanup failed"), "cleanup failed"},
		{"joined", &fakeCheckClient{startErr: errors.New("start failed"), stopErr: errors.New("stop failed")}, errors.New("cleanup failed"), "start bundled runtime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := testCheckDependencies(tc.client)
			deps.removeAll = func(string) error { return tc.remove }
			_, err := check(context.Background(), deps)
			if err == nil || !strings.Contains(err.Error(), tc.want) || tc.client.stops != 1 {
				t.Fatalf("check error = %v, stops=%d", err, tc.client.stops)
			}
			if tc.name == "joined" && (!strings.Contains(err.Error(), "stop failed") || !strings.Contains(err.Error(), "cleanup failed")) {
				t.Fatalf("errors were not joined: %v", err)
			}
		})
	}
}

func testCheckDependencies(client *fakeCheckClient) checkDependencies {
	return checkDependencies{
		path:        func() (string, error) { return "/bundle/copilot-runtime", nil },
		mkdirTemp:   func(string, string) (string, error) { return "/tmp/sodapop-runtime-check", nil },
		removeAll:   func(string) error { return nil },
		environment: func() []string { return nil },
		newClient:   func(*copilot.ClientOptions) checkClient { return client },
	}
}

type fakeCheckClient struct {
	status                       *copilot.GetStatusResponse
	startErr, statusErr, stopErr error
	starts, stops                int
}

func (c *fakeCheckClient) Start(context.Context) error {
	c.starts++
	return c.startErr
}

func (c *fakeCheckClient) GetStatus(context.Context) (*copilot.GetStatusResponse, error) {
	if c.status == nil && c.statusErr == nil {
		return nil, errors.New("missing status")
	}
	return c.status, c.statusErr
}

func (c *fakeCheckClient) Stop() error {
	c.stops++
	return c.stopErr
}

func containsEnvironment(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestBundledRuntimeSmoke(t *testing.T) {
	if os.Getenv("SODAPOP_RUNTIME_SMOKE") != "1" {
		t.Skip("set SODAPOP_RUNTIME_SMOKE=1 after bundling to exercise the native runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := Check(ctx); err != nil {
		t.Fatal(err)
	}
}
