package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type settings struct {
	releaseDir string
	manifest   string
	tool       string
}

type releaseManifest struct {
	Version string `json:"version"`
	SDK     string `json:"copilot_sdk_version"`
	Runtime string `json:"copilot_runtime_version"`
	Assets  []struct {
		Platform string `json:"platform"`
		Digest   string `json:"binary_sha256"`
	} `json:"artifacts"`
}

type commandRunner func(context.Context, string, []string, []string) (string, error)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr, execute); err != nil {
		fmt.Fprintln(os.Stderr, "Sodapop installation check:", err)
		os.Exit(1)
	}
}

func run(args []string, output, errorOutput io.Writer, runner commandRunner) error {
	flags := flag.NewFlagSet("installcheck", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	var options settings
	flags.StringVar(&options.releaseDir, "release-dir", "", "Directory containing release archives")
	flags.StringVar(&options.manifest, "manifest", "", "Release manifest to verify")
	flags.StringVar(&options.tool, "tool", "", "Native releasectl executable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || options.releaseDir == "" || options.manifest == "" || options.tool == "" {
		return errors.New("--release-dir, --manifest, and --tool are required; positional arguments are not supported")
	}
	for _, value := range []*string{&options.releaseDir, &options.manifest, &options.tool} {
		absolute, err := filepath.Abs(*value)
		if err != nil {
			return err
		}
		*value = absolute
	}
	if err := check(options, runner); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "Installed archive checks passed for %s/%s.\n", runtime.GOOS, runtime.GOARCH)
	return err
}

func execute(ctx context.Context, executable string, args, environment []string) (string, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = environment
	command.WaitDelay = 5 * time.Second
	data, err := command.CombinedOutput()
	return string(data), err
}

func check(options settings, runner commandRunner) (err error) {
	root, err := os.MkdirTemp("", "sodapop-install-check-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(root)) }()
	extracted := filepath.Join(root, "extracted")
	if err := os.Mkdir(extracted, 0700); err != nil {
		return err
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	helperOutput, err := runner(ctx, options.tool, []string{
		"extract", "--dir", options.releaseDir, "--manifest", options.manifest,
		"--platform", platform, "--output", extracted,
	}, os.Environ())
	if err != nil {
		return fmt.Errorf("verify and extract native archive: %w: %s", err, helperOutput)
	}
	manifest, err := readManifest(options.manifest)
	if err != nil {
		return err
	}
	expectedDigest := ""
	for _, asset := range manifest.Assets {
		if asset.Platform == platform {
			if expectedDigest != "" {
				return fmt.Errorf("duplicate platform %s in manifest", platform)
			}
			expectedDigest = asset.Digest
		}
	}
	if decoded, decodeErr := hex.DecodeString(expectedDigest); decodeErr != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("manifest has no valid native binary digest for %s", platform)
	}
	name := "sodapop"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	packageRoot := "sodapop-" + manifest.Version + "-" + runtime.GOOS + "-" + runtime.GOARCH
	archived := filepath.Join(extracted, packageRoot, name)
	if err := checkHash(archived, expectedDigest); err != nil {
		return err
	}
	home := filepath.Join(root, "home")
	environment, err := isolatedEnvironment(home, runtime.GOOS, os.Getenv)
	if err != nil {
		return err
	}
	for _, dir := range []string{home, filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	if err := smoke(archived, manifest, environment, runner); err != nil {
		return err
	}

	// A copied portable installation must work outside the extracted archive,
	// including user-selected paths containing spaces and Unicode characters.
	installed := filepath.Join(root, "portable \u00e9 with spaces", name)
	if err := copyCommand(archived, installed); err != nil {
		return err
	}
	if err := checkHash(installed, expectedDigest); err != nil {
		return err
	}
	return smoke(installed, manifest, environment, runner)
}

func readManifest(path string) (releaseManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return releaseManifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1024*1024))
	var manifest releaseManifest
	if err := decoder.Decode(&manifest); err != nil {
		return releaseManifest{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return releaseManifest{}, errors.New("manifest contains trailing data")
	}
	if manifest.Version == "" || strings.ContainsAny(manifest.Version, `/\`) || manifest.SDK == "" || manifest.Runtime == "" {
		return releaseManifest{}, errors.New("manifest is missing valid version metadata")
	}
	return manifest, nil
}

func checkHash(path, expected string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("installed command is not a nonempty regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, readErr := io.Copy(digest, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected {
		return errors.New("installed binary does not match the release manifest")
	}
	return nil
}

func copyCommand(source, destination string) (err error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Close())
}

func smoke(executable string, manifest releaseManifest, environment []string, runner commandRunner) error {
	for _, argument := range []string{"--version", "--help", "--check-runtime", "--check-runtime"} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		output, err := runner(ctx, executable, []string{argument}, environment)
		cancel()
		if err != nil {
			return fmt.Errorf("installed command %s: %w: %s", argument, err, output)
		}
		normalized := strings.ReplaceAll(output, "\r\n", "\n")
		switch argument {
		case "--version":
			expected := fmt.Sprintf("sodapop %s\nCopilot SDK %s / runtime %s\n", manifest.Version, manifest.SDK, manifest.Runtime)
			if normalized != expected {
				return fmt.Errorf("installed version output differs from the release manifest: %q", normalized)
			}
		case "--help":
			if !strings.Contains(normalized, "Usage: sodapop") {
				return errors.New("installed help did not describe the Sodapop command")
			}
		case "--check-runtime":
			if !strings.HasPrefix(normalized, "Copilot "+manifest.Runtime+" (protocol ") {
				return errors.New("installed runtime did not report the pinned protocol handshake")
			}
		}
	}
	return nil
}

func isolatedEnvironment(home, goos string, getenv func(string) string) ([]string, error) {
	values := []string{
		"HOME=" + home, "USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"XDG_STATE_HOME=" + filepath.Join(home, "state"),
		"XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
		"APPDATA=" + filepath.Join(home, "appdata"),
		"LOCALAPPDATA=" + filepath.Join(home, "localappdata"),
		"TMPDIR=" + filepath.Join(home, "tmp"),
		"TMP=" + filepath.Join(home, "tmp"), "TEMP=" + filepath.Join(home, "tmp"),
		"NO_COLOR=1", "TERM=dumb",
		"SODAPOP_LIVE_QUALIFY=0", "SODAPOP_RUNTIME_SMOKE=0",
	}
	if goos == "windows" {
		systemRoot := getenv("SystemRoot")
		if systemRoot == "" {
			return nil, errors.New("SystemRoot is required for native Windows installation checks")
		}
		values = append(values, "SystemRoot="+systemRoot, "WINDIR="+systemRoot,
			"PATH="+filepath.Join(systemRoot, "System32"),
			"PATHEXT=.COM;.EXE;.BAT;.CMD")
	} else {
		values = append(values, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
	}
	return values, nil
}
