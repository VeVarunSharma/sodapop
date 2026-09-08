package runtimebundle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/embeddedcli"
)

var ErrUnavailable = errors.New("the bundled Copilot runtime is unavailable")

type checkClient interface {
	Start(context.Context) error
	GetStatus(context.Context) (*copilot.GetStatusResponse, error)
	Stop() error
}

type checkDependencies struct {
	path        func() (string, error)
	mkdirTemp   func(string, string) (string, error)
	removeAll   func(string) error
	environment func() []string
	newClient   func(*copilot.ClientOptions) checkClient
}

func defaultCheckDependencies() checkDependencies {
	return checkDependencies{
		path:        Path,
		mkdirTemp:   os.MkdirTemp,
		removeAll:   os.RemoveAll,
		environment: Environment,
		newClient: func(options *copilot.ClientOptions) checkClient {
			return copilot.NewClient(options)
		},
	}
}

func Path() (string, error) {
	return resolve(embeddedcli.Path, runtime.GOOS, runtime.GOARCH)
}

func resolve(install func() string, goos, goarch string) (string, error) {
	if !supportedPlatform(goos, goarch) {
		return "", fmt.Errorf("%w: unsupported platform %s/%s", ErrUnavailable, goos, goarch)
	}
	entry := install()
	if entry == "" {
		return "", fmt.Errorf("%w: rebuild with `make build`, or reinstall the platform release and check the runtime cache permissions", ErrUnavailable)
	}
	dir := filepath.Dir(entry)
	executable := filepath.Join(dir, runtimeExecutableName(goos))
	for _, path := range []string{executable, filepath.Join(dir, "runtime.node")} {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return "", fmt.Errorf("%w: invalid artifact %s", ErrUnavailable, filepath.Base(path))
		}
		if goos != "windows" && path == executable && info.Mode().Perm()&0111 == 0 {
			return "", fmt.Errorf("%w: runtime executable has no execute permission", ErrUnavailable)
		}
	}
	return executable, nil
}

func supportedPlatform(goos, goarch string) bool {
	return (goos == "darwin" || goos == "linux") && (goarch == "arm64" || goarch == "amd64") ||
		goos == "windows" && goarch == "amd64"
}

func runtimeExecutableName(goos string) string {
	if goos == "windows" {
		return "copilot-runtime.exe"
	}
	return "copilot-runtime"
}

func Environment() []string {
	return cleanEnvironment(os.Environ())
}

func cleanEnvironment(values []string) []string {
	return cleanEnvironmentForPlatform(values, runtime.GOOS)
}

func cleanEnvironmentForPlatform(values []string, goos string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		if goos == "windows" {
			key = strings.ToUpper(key)
		}
		if strings.HasPrefix(key, "COPILOT_") || key == "GH_TOKEN" || key == "GITHUB_TOKEN" || key == "GH_HOST" {
			continue
		}
		result = append(result, value)
	}
	return result
}

// Check exercises the packaged process and protocol without opening an agent session.
func Check(ctx context.Context) (version string, err error) {
	return check(ctx, defaultCheckDependencies())
}

func check(ctx context.Context, deps checkDependencies) (version string, err error) {
	path, err := deps.path()
	if err != nil {
		return "", err
	}
	home, err := deps.mkdirTemp("", "sodapop-runtime-check-")
	if err != nil {
		return "", fmt.Errorf("create runtime check directory: %w", err)
	}
	defer func() {
		err = errors.Join(err, deps.removeAll(home))
	}()
	client := deps.newClient(&copilot.ClientOptions{
		Connection:       copilot.StdioConnection{Path: path},
		BaseDirectory:    home,
		WorkingDirectory: home,
		Mode:             copilot.ModeEmpty,
		UseLoggedInUser:  copilot.Bool(false),
		Env:              deps.environment(),
		LogLevel:         "none",
	})
	defer func() {
		err = errors.Join(err, client.Stop())
	}()
	if err := client.Start(ctx); err != nil {
		return "", fmt.Errorf("start bundled runtime: %w", err)
	}
	status, err := client.GetStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("query bundled runtime: %w", err)
	}
	if strings.TrimPrefix(status.Version, "v") != Version {
		return "", fmt.Errorf("bundled runtime version mismatch: expected %s, received %s", Version, status.Version)
	}
	return fmt.Sprintf("Copilot %s (protocol %d)", status.Version, status.ProtocolVersion), nil
}
