package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

var (
	homebrewTargets   = []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"}
	homebrewPlatforms = []string{"darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64"}
)

func websiteIsolationFiles() []string {
	return []string{
		"site/README.md",
		"site/package.json",
		"site/package-lock.json",
		"site/public/licenses/NOTICE.txt",
		"site/src/pages/download.astro",
	}
}

type homebrewManifest struct {
	SchemaVersion         int                     `json:"schema_version"`
	Version               string                  `json:"version"`
	Commit                string                  `json:"commit"`
	CopilotSDKVersion     string                  `json:"copilot_sdk_version"`
	CopilotRuntimeVersion string                  `json:"copilot_runtime_version"`
	Artifacts             []homebrewManifestAsset `json:"artifacts"`
}

type homebrewManifestAsset struct {
	Platform      string `json:"platform"`
	Archive       string `json:"archive"`
	ArchiveSHA256 string `json:"archive_sha256"`
	BinarySHA256  string `json:"binary_sha256"`
}

type homebrewArchiveEntry struct {
	Mode    fs.FileMode
	Content []byte
}

func homebrewWriteFixtureFile(t *testing.T, root, name, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func homebrewRunScriptFixture(t *testing.T, root string, args []string, env ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "SODAPOP_") ||
			slices.Contains([]string{
				"PATH", "HOME", "PWD", "OLDPWD", "BASH_ENV", "ENV", "MAKEFLAGS", "MFLAGS", "MAKELEVEL", "GNUMAKEFLAGS", "MAKEFILES",
			}, name) {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "HOME="+filepath.Join(root, "home"))
	cmd.Env = append(cmd.Env, "TMPDIR="+filepath.Join(root, "tmp"))
	cmd.Env = append(cmd.Env, "PATH="+filepath.Join(root, "fake-bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, env...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func homebrewAssertFixtureLines(t *testing.T, root, name string, want []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"); !slices.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func homebrewSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func homebrewWriteTarGzip(t *testing.T, name string, entries map[string]homebrewArchiveEntry) {
	t.Helper()
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(entries))
	for entry := range entries {
		names = append(names, entry)
	}
	slices.Sort(names)
	for _, entry := range names {
		value := entries[entry]
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: entry,
			Mode: int64(value.Mode.Perm()),
			Size: int64(len(value.Content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(value.Content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func homebrewFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{
		"scripts/generate-homebrew-formula.sh",
		"scripts/test-homebrew-install.sh",
		"scripts/homebrew/common.sh",
		"packaging/homebrew/Formula/sodapop.rb.tmpl",
		"internal/runtimebundle/version.go",
	} {
		data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		homebrewWriteFixtureFile(t, root, name, string(data), 0600)
	}
	return root
}

func homebrewDigest(index int) string {
	return strings.Repeat(fmt.Sprintf("%x", index+1), 64)
}

func homebrewExpectedHelp() string {
	return `Sodapop - a neon terminal coding companion

Usage: sodapop [options]

Run sodapop from your project directory. Type / for commands or /login for your account.

Commands: /help /login /logout /model /clear /resume /compact /plan /diff /theme /exit

Sign-in uses Sodapop's own GitHub OAuth device flow. Development builds need
SODAPOP_GITHUB_CLIENT_ID set to a registered, device-flow-enabled public client ID.

Options:
  -ascii
        use ASCII terminal presentation
  -check-runtime
        check the bundled runtime without signing in or calling a model
  -h    show help
  -help
        show help without starting the agent
  -no-banner
        skip the startup mascot banner
  -no-color
        disable terminal colors
  -reduced-motion
        disable decorative animation
  -v    show versions
  -version
        show Sodapop, SDK, and runtime versions
`
}

func writeHomebrewChecksums(t *testing.T, root, version string) string {
	t.Helper()
	directory := filepath.Join(root, "checksums")
	for index, target := range homebrewTargets {
		archive := fmt.Sprintf("sodapop-%s-%s.tar.gz", version, target)
		homebrewWriteFixtureFile(t, root, "checksums/"+archive+".sha256",
			homebrewDigest(index)+"  "+archive+"\n", 0600)
	}
	return directory
}

func homebrewFixtureExecutable(version, sdkVersion, runtimeVersion string) string {
	replacer := strings.NewReplacer(
		"VERSION_PLACEHOLDER", version,
		"SDK_PLACEHOLDER", sdkVersion,
		"RUNTIME_PLACEHOLDER", runtimeVersion,
		"HELP_PLACEHOLDER", homebrewExpectedHelp(),
	)
	return replacer.Replace(`#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  --version)
    printf 'sodapop VERSION_PLACEHOLDER\nCopilot SDK SDK_PLACEHOLDER / runtime RUNTIME_PLACEHOLDER\n'
    ;;
  --help)
    cat <<'EOF'
HELP_PLACEHOLDEREOF
    ;;
  --check-runtime)
    printf 'Copilot RUNTIME_PLACEHOLDER (protocol fixture-protocol) is ready\n'
    ;;
  *)
    printf 'unexpected fixture invocation: %s\n' "${1:-}" >&2
    exit 9
    ;;
esac
`)
}

func homebrewHostTarget(t *testing.T) string {
	t.Helper()
	target := runtime.GOOS + "/" + runtime.GOARCH
	switch target {
	case "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64":
		return strings.ReplaceAll(target, "/", "-")
	default:
		t.Skipf("native Homebrew flow requires one of the four UNIX release targets, got %s", target)
		return ""
	}
}

func writeHomebrewReleaseInputs(
	t *testing.T,
	root, directory, version, sdkVersion, runtimeVersion string,
) (string, string, homebrewManifest) {
	t.Helper()
	releaseDir := filepath.Join(root, directory)
	if err := os.MkdirAll(releaseDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := homebrewManifest{
		SchemaVersion:         1,
		Version:               version,
		Commit:                strings.Repeat("a", 40),
		CopilotSDKVersion:     sdkVersion,
		CopilotRuntimeVersion: runtimeVersion,
	}
	for index, platform := range homebrewPlatforms {
		packageRoot := "sodapop-" + version + "-" + strings.ReplaceAll(platform, "/", "-")
		archive := packageRoot + ".tar.gz"
		executable := []byte(homebrewFixtureExecutable(version, sdkVersion, runtimeVersion))
		homebrewWriteTarGzip(t, filepath.Join(releaseDir, archive), map[string]homebrewArchiveEntry{
			packageRoot + "/sodapop":                          {Mode: 0755, Content: executable},
			packageRoot + "/LICENSE":                          {Mode: 0644, Content: []byte("fixture license")},
			packageRoot + "/THIRD_PARTY_NOTICES.md":           {Mode: 0644, Content: []byte("fixture notices")},
			packageRoot + "/LICENSES/copilot-runtime.license": {Mode: 0644, Content: []byte("fixture runtime terms")},
			packageRoot + "/LICENSES/dependency.license":      {Mode: 0644, Content: []byte("fixture dependency terms")},
		})
		data, err := os.ReadFile(filepath.Join(releaseDir, archive))
		if err != nil {
			t.Fatal(err)
		}
		digest := homebrewSHA256(data)
		homebrewWriteFixtureFile(t, releaseDir, archive+".sha256", digest+"  "+archive+"\n", 0600)
		manifest.Artifacts = append(manifest.Artifacts, homebrewManifestAsset{
			Platform:      platform,
			Archive:       archive,
			ArchiveSHA256: digest,
			BinarySHA256:  homebrewSHA256(executable),
		})
		if index == 0 {
			homebrewWriteFixtureFile(t, releaseDir, "marker", "fixture", 0600)
		}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(releaseDir, "manifest.json")
	homebrewWriteFixtureFile(t, releaseDir, "manifest.json", string(data)+"\n", 0600)
	return releaseDir, manifestPath, manifest
}

func writeHomebrewPublicCurl(t *testing.T, root string, manifest homebrewManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	homebrewWriteFixtureFile(t, root, "manifest.json", string(data), 0600)
	homebrewWriteFixtureFile(t, root, "fake-bin/curl", `#!/usr/bin/env bash
set -euo pipefail
if [[ "$#" != 2 || "$1" != "-fsSL" ]]; then
  printf 'unexpected curl invocation\n' >&2
  exit 1
fi
printf '%s\n' "$2" >> curl.log
case "$2" in
  *-manifest.json) cat manifest.json ;;
  */*.sha256)
    sidecar="${2##*/}"
    archive="${sidecar%.sha256}"
    case "$archive" in
      *-darwin-arm64.tar.gz) digit=1 ;;
      *-darwin-amd64.tar.gz) digit=2 ;;
      *-linux-arm64.tar.gz) digit=3 ;;
      *-linux-amd64.tar.gz) digit=4 ;;
      *) printf 'unexpected release asset: %s\n' "$archive" >&2; exit 1 ;;
    esac
    printf '%64s' '' | tr ' ' "$digit"
    printf '  %s\n' "$archive"
    ;;
  *)
    printf 'unexpected curl URL\n' >&2
    exit 1
    ;;
esac
`, 0700)
}

func writeHomebrewReleaseHelper(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "fake-bin", "releasectl")
	homebrewWriteFixtureFile(t, root, "fake-bin/releasectl", `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> releasectl.log
`, 0700)
	return path
}

func writeFakeHomebrew(t *testing.T, root string, prefix string) {
	t.Helper()
	homebrewWriteFixtureFile(t, root, "fake-bin/brew", `#!/usr/bin/env bash
set -euo pipefail
prefix="${FAKE_BREW_PREFIX:?missing fake prefix}"
state="${FAKE_BREW_STATE:?missing fake state}"
platform="${FAKE_BREW_PLATFORM:?missing fake platform}"
log="${FAKE_BREW_LOG:?missing fake log}"
mkdir -p "$state"
printf '%s\n' "$*" >> "$log"

decode_file_url() {
  ruby -ruri -e 'puts URI(ARGV.fetch(0)).path' "$1"
}

tap_formula_path() {
  [[ -f "$state/tapped-repo" ]] || exit 6
  printf '%s/Formula/sodapop.rb\n' "$(<"$state/tapped-repo")"
}

formula_version() {
  sed -n 's/^  version "\(.*\)"/\1/p' "$(tap_formula_path)"
}

formula_archive_url() {
  grep -o "file://[^\"]*-${platform}\.tar\.gz" "$(tap_formula_path)" | head -n 1
}

install_formula() {
  version="$(formula_version)"
  url="$(formula_archive_url)"
  if [[ -z "$version" || -z "$url" ]]; then
    printf 'invalid fixture formula\n' >&2
    exit 3
  fi
  if [[ "${FAKE_BREW_COLLISION_AFTER_KEG:-0}" == "1" && ! -f "$state/collision-after-keg.done" ]]; then
    mkdir -p "$prefix/Cellar/sodapop/$version"
    : > "$state/collision-after-keg.done"
    printf '%s\n' "$version" > "$state/version"
    printf 'late collision\n' >&2
    exit 1
  fi
  if [[ -e "$prefix/bin/sodapop" && ! -L "$prefix/bin/sodapop" ]]; then
    printf 'collision\n' >&2
    exit 1
  fi
  archive="$(decode_file_url "$url")"
  package_root="sodapop-${version}-${platform}"
  mkdir -p "$prefix/bin" "$prefix/share" "$prefix/Cellar/sodapop/$version"
  if [[ "${FAKE_BREW_REPLACE_COLLISION_WITH_SYMLINK:-0}" == "1" ]]; then
    rm -f "$prefix/bin/sodapop"
    mkdir -p "$prefix/Cellar/collision"
    ln -s "$prefix/Cellar/collision/replaced" "$prefix/bin/sodapop"
    printf 'link failure\n' >&2
    exit 1
  fi
  rm -rf "$prefix/Cellar/sodapop/$version" "$prefix/share/sodapop" "$prefix/bin/sodapop"
  mkdir -p "$prefix/Cellar/sodapop/$version"
  tar -xzf "$archive" -C "$prefix/Cellar/sodapop/$version"
  ln -s "$prefix/Cellar/sodapop/$version/$package_root/sodapop" "$prefix/bin/sodapop"
  mkdir -p "$prefix/share/sodapop"
  cp "$prefix/Cellar/sodapop/$version/$package_root/LICENSE" "$prefix/share/sodapop/LICENSE"
  cp "$prefix/Cellar/sodapop/$version/$package_root/THIRD_PARTY_NOTICES.md" "$prefix/share/sodapop/THIRD_PARTY_NOTICES.md"
  cp -R "$prefix/Cellar/sodapop/$version/$package_root/LICENSES" "$prefix/share/sodapop/LICENSES"
  if [[ "${FAKE_BREW_TAMPER:-0}" == "1" ]]; then
    printf '\n# tampered\n' >> "$prefix/Cellar/sodapop/$version/$package_root/sodapop"
  fi
  printf '%s\n' "$version" > "$state/version"
}

case "$1" in
  --repository)
    [[ "${2:-}" == "sodapop/test" ]] || exit 8
    [[ -f "$state/tapped-repo" ]] || exit 8
    cat "$state/tapped-repo"
    ;;
  --prefix)
    printf '%s\n' "$prefix"
    ;;
  tap)
    if [[ $# == 1 ]]; then
      if [[ -f "$state/tapped-repo" ]]; then printf 'sodapop/test\n'; fi
      exit 0
    fi
    source_repo="${@: -1}"
    clone_repo="$state/tapped-repo-clone"
    rm -rf "$clone_repo"
    git clone -q "$source_repo" "$clone_repo"
    printf '%s\n' "$clone_repo" > "$state/tapped-repo"
    ;;
  untap)
    if [[ -f "$state/tapped-repo" ]]; then
      rm -rf "$(<"$state/tapped-repo")"
    fi
    rm -f "$state/tapped-repo"
    ;;
  list)
    if [[ "${FAKE_BREW_FAIL_LIST:-0}" == "1" ]]; then
      printf 'list failure\n' >&2
      exit 1
    fi
    if [[ "${2:-}" == "--versions" && "${3:-}" == "sodapop" && -f "$state/version" ]]; then
      printf 'sodapop %s\n' "$(<"$state/version")"
    fi
    if [[ "${2:-}" == "--formula" && -f "$state/version" ]]; then
      printf 'sodapop\n'
    fi
    ;;
  install|reinstall|upgrade)
    install_formula
    ;;
  test)
    [[ "${2:-}" == "sodapop" ]] || exit 4
    ;;
  uninstall)
    if [[ "${FAKE_BREW_FAIL_UNINSTALL:-0}" == "1" ]]; then
      printf 'uninstall failure\n' >&2
      exit 1
    fi
    rm -f "$prefix/bin/sodapop"
    rm -rf "$prefix/share/sodapop" "$prefix/Cellar/sodapop"
    rm -f "$state/version"
    ;;
  *)
    printf 'unexpected brew invocation: %s\n' "$*" >&2
    exit 5
    ;;
esac
`, 0700)
}

func TestGenerateHomebrewFormulaUsesTaggedReleaseAssets(t *testing.T) {
	root := homebrewFixture(t)
	version := "1.2.3-rc.1"
	manifest := homebrewManifest{
		SchemaVersion:         1,
		Version:               version,
		Commit:                strings.Repeat("b", 40),
		CopilotSDKVersion:     "1.0.13",
		CopilotRuntimeVersion: "1.0.83",
	}
	for index, platform := range homebrewPlatforms {
		manifest.Artifacts = append(manifest.Artifacts, homebrewManifestAsset{
			Platform:      platform,
			Archive:       "sodapop-" + version + "-" + strings.ReplaceAll(platform, "/", "-") + ".tar.gz",
			ArchiveSHA256: homebrewDigest(index),
			BinarySHA256:  strings.Repeat("f", 64),
		})
	}
	writeHomebrewPublicCurl(t, root, manifest)
	output := filepath.Join(root, "tap", "Formula", "sodapop.rb")
	result, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/generate-homebrew-formula.sh", version, output})
	if err != nil || !strings.Contains(result, "Generated Homebrew formula for v"+version) {
		t.Fatalf("formula generation failed: %s: %v", result, err)
	}

	formula, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(formula)
	for index, target := range homebrewTargets {
		archive := fmt.Sprintf("sodapop-%s-%s.tar.gz", version, target)
		url := "https://github.com/VeVarunSharma/sodapop/releases/download/v" + version + "/" + archive
		if !strings.Contains(text, `url "`+url+`"`) ||
			!strings.Contains(text, `sha256 "`+homebrewDigest(index)+`"`) {
			t.Errorf("formula is missing release data for %s", target)
		}
	}
	for _, snippet := range []string{
		`license all_of: ["MIT", :cannot_represent]`,
		`pkgshare.install "LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSES"`,
		`assert_path_exists pkgshare/"LICENSES"/"copilot-runtime.license"`,
		`assert_predicate Dir[pkgshare/"LICENSES"/"**"/"*"].reject { |path| File.directory?(path) || path.end_with?("/copilot-runtime.license") }, :any?`,
		`Copilot SDK 1.0.13 / runtime 1.0.83`,
		`help = shell_output("#{bin}/sodapop --help")`,
		`assert_includes help, "Usage: sodapop [options]"`,
		`Commands: /help /login /logout /model /clear /resume /compact /plan /diff /theme /exit`,
		`assert_includes help, "-check-runtime"`,
	} {
		if !strings.Contains(text, snippet) {
			t.Fatalf("formula is missing expected Homebrew content %q:\n%s", snippet, text)
		}
	}
	if strings.Contains(text, "@VERSION@") || strings.Contains(text, "@COPILOT_RUNTIME_VERSION@") {
		t.Fatalf("formula has unresolved placeholders:\n%s", text)
	}

	homebrewAssertFixtureLines(t, root, "curl.log", []string{
		"https://github.com/VeVarunSharma/sodapop/releases/download/v" + version + "/sodapop-" + version + "-manifest.json",
		"https://github.com/VeVarunSharma/sodapop/releases/download/v" + version + "/sodapop-" + version + "-darwin-arm64.tar.gz.sha256",
		"https://github.com/VeVarunSharma/sodapop/releases/download/v" + version + "/sodapop-" + version + "-darwin-amd64.tar.gz.sha256",
		"https://github.com/VeVarunSharma/sodapop/releases/download/v" + version + "/sodapop-" + version + "-linux-arm64.tar.gz.sha256",
		"https://github.com/VeVarunSharma/sodapop/releases/download/v" + version + "/sodapop-" + version + "-linux-amd64.tar.gz.sha256",
	})

	result, err = homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/generate-homebrew-formula.sh", "--check", version, output})
	if err != nil || !strings.Contains(result, "Homebrew formula is current for v"+version) {
		t.Fatalf("current formula check failed: %s: %v", result, err)
	}
	if err := os.WriteFile(output, append(formula, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/generate-homebrew-formula.sh", "--check", version, output})
	if err == nil || !strings.Contains(result, "Homebrew formula is stale for v"+version) {
		t.Fatalf("stale formula was accepted: %s: %v", result, err)
	}
}

func TestGenerateHomebrewFormulaUsesVerifiedLocalManifest(t *testing.T) {
	root := homebrewFixture(t)
	releaseDir, manifestPath, manifest := writeHomebrewReleaseInputs(t, root, `release #1 & "quotes" café`, "1.2.3", "1.0.13", "1.0.83")
	helper := writeHomebrewReleaseHelper(t, root)
	output := filepath.Join(root, "tap", "Formula", "sodapop.rb")
	result, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/generate-homebrew-formula.sh", manifest.Version, output},
		"SODAPOP_RELEASECTL="+helper,
		"SODAPOP_HOMEBREW_RELEASE_DIR="+releaseDir,
		"SODAPOP_HOMEBREW_MANIFEST="+manifestPath,
		"SODAPOP_HOMEBREW_URL_BASE=file://"+filepath.ToSlash(releaseDir))
	if err != nil || !strings.Contains(result, "Generated Homebrew formula for v"+manifest.Version) {
		t.Fatalf("manifest-backed formula generation failed: %s: %v", result, err)
	}
	formula, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(formula)
	canonicalReleaseDir, err := filepath.EvalSymlinks(releaseDir)
	if err != nil {
		t.Fatal(err)
	}
	encodedReleaseDir := strings.NewReplacer("%", "%25", " ", "%20", "#", "%23", "&", "%26", `"`, "%22", "é", "%C3%A9").Replace(filepath.ToSlash(canonicalReleaseDir))
	if !strings.Contains(text, `url "file:///`+strings.TrimPrefix(encodedReleaseDir, "/")+`/sodapop-1.2.3-darwin-arm64.tar.gz"`) ||
		strings.Contains(text, `https://github.com/VeVarunSharma/sodapop/releases/download/`) {
		t.Fatalf("formula did not use the local release base:\n%s", text)
	}
	canonicalManifestPath, err := filepath.EvalSymlinks(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"verify --dir " + canonicalReleaseDir + " --manifest " + canonicalManifestPath + " --platform darwin/arm64",
		"verify --dir " + canonicalReleaseDir + " --manifest " + canonicalManifestPath + " --platform darwin/amd64",
		"verify --dir " + canonicalReleaseDir + " --manifest " + canonicalManifestPath + " --platform linux/arm64",
		"verify --dir " + canonicalReleaseDir + " --manifest " + canonicalManifestPath + " --platform linux/amd64",
	} {
		if !strings.Contains(readFixtureFile(t, root, "releasectl.log"), want) {
			t.Fatalf("release helper log did not include %q", want)
		}
	}
}

func readFixtureFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGenerateHomebrewFormulaValidatesVersionAndChecksums(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		setup   func(t *testing.T, root string) []string
		want    string
	}{
		{
			name:    "tag prefix in version",
			version: "v1.2.3",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{"SODAPOP_HOMEBREW_CHECKSUM_DIR=" + writeHomebrewChecksums(t, root, "1.2.3")}
			},
			want: "Invalid Homebrew release version",
		},
		{
			name:    "missing manifest pair",
			version: "1.2.3",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{"SODAPOP_HOMEBREW_RELEASE_DIR=" + filepath.Join(root, "release")}
			},
			want: "SODAPOP_HOMEBREW_MANIFEST and SODAPOP_HOMEBREW_RELEASE_DIR must be provided together",
		},
		{
			name:    "version mismatch in manifest",
			version: "1.2.3",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.4", "1.0.13", "1.0.83")
				return []string{
					"SODAPOP_RELEASECTL=" + writeHomebrewReleaseHelper(t, root),
					"SODAPOP_HOMEBREW_RELEASE_DIR=" + releaseDir,
					"SODAPOP_HOMEBREW_MANIFEST=" + manifestPath,
				}
			},
			want: "does not match requested version",
		},
		{
			name:    "template alias output",
			version: "1.2.3",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{"SODAPOP_HOMEBREW_CHECKSUM_DIR=" + writeHomebrewChecksums(t, root, "1.2.3")}
			},
			want: "Refusing to overwrite the Homebrew formula template",
		},
		{
			name:    "missing target",
			version: "1.2.3",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				checksums := writeHomebrewChecksums(t, root, "1.2.3")
				if err := os.Remove(filepath.Join(checksums, "sodapop-1.2.3-linux-amd64.tar.gz.sha256")); err != nil {
					t.Fatal(err)
				}
				return []string{"SODAPOP_HOMEBREW_CHECKSUM_DIR=" + checksums}
			},
			want: "Missing Homebrew checksum sidecar",
		},
		{
			name:    "remote checksum mismatch",
			version: "1.2.3",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				manifest := homebrewManifest{
					SchemaVersion:         1,
					Version:               "1.2.3",
					Commit:                strings.Repeat("c", 40),
					CopilotSDKVersion:     "1.0.13",
					CopilotRuntimeVersion: "1.0.83",
				}
				for index, platform := range homebrewPlatforms {
					digest := homebrewDigest(index)
					if platform == "linux/amd64" {
						digest = strings.Repeat("e", 64)
					}
					manifest.Artifacts = append(manifest.Artifacts, homebrewManifestAsset{
						Platform:      platform,
						Archive:       "sodapop-1.2.3-" + strings.ReplaceAll(platform, "/", "-") + ".tar.gz",
						ArchiveSHA256: digest,
						BinarySHA256:  strings.Repeat("f", 64),
					})
				}
				writeHomebrewPublicCurl(t, root, manifest)
				return nil
			},
			want: "Tagged release checksum for linux/amd64 does not match the manifest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := homebrewFixture(t)
			env := tc.setup(t, root)
			output := filepath.Join(root, "Formula", "sodapop.rb")
			args := []string{"bash", "scripts/generate-homebrew-formula.sh", tc.version, output}
			if tc.name == "template alias output" {
				args[3] = "./packaging/homebrew/Formula/../Formula/sodapop.rb.tmpl"
			}
			result, err := homebrewRunScriptFixture(t, root, args, env...)
			if err == nil || !strings.Contains(result, tc.want) {
				t.Fatalf("invalid release data was accepted: %s: %v", result, err)
			}
			if tc.name != "template alias output" {
				if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
					t.Fatalf("invalid release data produced a formula: %v", statErr)
				}
			}
		})
	}
}

func TestHomebrewInstallScriptBlocksOrdinaryPrefixWithoutOptIn(t *testing.T) {
	for _, tc := range []struct {
		name         string
		prefix       string
		wantContains string
	}{
		{name: "default prefix", prefix: "/opt/homebrew", wantContains: "/opt/homebrew"},
		{name: "custom user prefix", prefix: "/home/linuxbrew/.linuxbrew/custom", wantContains: "home/linuxbrew/.linuxbrew/custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := homebrewFixture(t)
			releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.3", "1.0.13", "1.0.83")
			homebrewWriteFixtureFile(t, root, "fake-bin/brew", `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  --prefix) printf '`+tc.prefix+`\n' ;;
  list) ;;
  *) printf 'unexpected brew invocation\n' >&2; exit 7 ;;
esac
`, 0700)
			output, err := homebrewRunScriptFixture(t, root,
				[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releaseDir, "--manifest", manifestPath})
			if err == nil || !strings.Contains(output, "would mutate the Homebrew prefix ") || !strings.Contains(output, tc.wantContains) {
				t.Fatalf("prefix guard did not fire: %s: %v", output, err)
			}
		})
	}
}

func TestHomebrewInstallScriptFailsWhenInstalledFormulaQueryFails(t *testing.T) {
	root := homebrewFixture(t)
	releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.3", "1.0.13", "1.0.83")
	helper := writeHomebrewReleaseHelper(t, root)
	prefix := filepath.Join(root, "fake-prefix")
	writeFakeHomebrew(t, root, prefix)
	target := homebrewHostTarget(t)
	output, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releaseDir, "--manifest", manifestPath},
		"SODAPOP_RELEASECTL="+helper,
		"SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1",
		"FAKE_BREW_PREFIX="+prefix,
		"FAKE_BREW_STATE="+filepath.Join(root, "fake-brew-state"),
		"FAKE_BREW_PLATFORM="+target,
		"FAKE_BREW_LOG="+filepath.Join(root, "brew.log"),
		"FAKE_BREW_FAIL_LIST=1")
	if err == nil || !strings.Contains(output, "Could not query installed Homebrew formulae") {
		t.Fatalf("installed-formula query failure was swallowed: %s: %v", output, err)
	}
}

func TestHomebrewInstallScriptExercisesInstallReinstallUpgradeAndUninstall(t *testing.T) {
	root := homebrewFixture(t)
	currentRelease, currentManifestPath, _ := writeHomebrewReleaseInputs(t, root, "release-current", "1.2.4", "1.0.13", "1.0.83")
	previousRelease, previousManifestPath, _ := writeHomebrewReleaseInputs(t, root, "release-previous", "1.2.3", "1.0.13", "1.0.83")
	helper := writeHomebrewReleaseHelper(t, root)
	prefix := filepath.Join(root, "fake-prefix")
	writeFakeHomebrew(t, root, prefix)
	target := homebrewHostTarget(t)
	output, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/test-homebrew-install.sh",
			"--release-dir", currentRelease,
			"--manifest", currentManifestPath,
			"--previous-release-dir", previousRelease,
			"--previous-manifest", previousManifestPath},
		"SODAPOP_RELEASECTL="+helper,
		"SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1",
		"FAKE_BREW_PREFIX="+prefix,
		"FAKE_BREW_STATE="+filepath.Join(root, "fake-brew-state"),
		"FAKE_BREW_PLATFORM="+target,
		"FAKE_BREW_LOG="+filepath.Join(root, "brew.log"))
	if err != nil || !strings.Contains(output, "Homebrew install, upgrade, reinstall, and uninstall checks passed") {
		t.Fatalf("native install flow failed: %s: %v", output, err)
	}
	log := readFixtureFile(t, root, "brew.log")
	for _, want := range []string{
		"tap --custom-remote sodapop/test ",
		"--repository sodapop/test",
		"install --formula sodapop/test/sodapop",
		"upgrade --formula sodapop/test/sodapop",
		"reinstall --formula sodapop/test/sodapop",
		"uninstall --formula --force sodapop/test/sodapop",
		"untap --force sodapop/test",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("brew log did not include %q:\n%s", want, log)
		}
	}
	if strings.Count(log, "test sodapop") != 3 {
		t.Fatalf("brew log did not run brew test for each install stage:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "sodapop")); !os.IsNotExist(err) {
		t.Fatalf("uninstall left sodapop in the fake prefix: %v", err)
	}
}

func TestHomebrewInstallScriptRemovesCollisionKegBeforeMainInstall(t *testing.T) {
	root := homebrewFixture(t)
	releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.3", "1.0.13", "1.0.83")
	helper := writeHomebrewReleaseHelper(t, root)
	prefix := filepath.Join(root, "fake-prefix")
	writeFakeHomebrew(t, root, prefix)
	target := homebrewHostTarget(t)
	output, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releaseDir, "--manifest", manifestPath},
		"SODAPOP_RELEASECTL="+helper,
		"SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1",
		"FAKE_BREW_PREFIX="+prefix,
		"FAKE_BREW_STATE="+filepath.Join(root, "fake-brew-state"),
		"FAKE_BREW_PLATFORM="+target,
		"FAKE_BREW_LOG="+filepath.Join(root, "brew.log"),
		"FAKE_BREW_COLLISION_AFTER_KEG=1")
	if err != nil || !strings.Contains(output, "Homebrew install, reinstall, and uninstall checks passed") {
		t.Fatalf("late-collision cleanup failed: %s: %v", output, err)
	}
	log := readFixtureFile(t, root, "brew.log")
	if strings.Count(log, "uninstall --formula --force sodapop/test/sodapop") < 1 {
		t.Fatalf("collision cleanup did not uninstall the partial keg:\n%s", log)
	}
	if _, statErr := os.Stat(filepath.Join(prefix, "Cellar", "sodapop")); !os.IsNotExist(statErr) {
		t.Fatalf("collision cleanup left a Homebrew keg behind: %v", statErr)
	}
}

func TestHomebrewInstallScriptRejectsHashMismatch(t *testing.T) {
	root := homebrewFixture(t)
	releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.3", "1.0.13", "1.0.83")
	helper := writeHomebrewReleaseHelper(t, root)
	prefix := filepath.Join(root, "fake-prefix")
	writeFakeHomebrew(t, root, prefix)
	target := homebrewHostTarget(t)
	output, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releaseDir, "--manifest", manifestPath},
		"SODAPOP_RELEASECTL="+helper,
		"SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1",
		"FAKE_BREW_PREFIX="+prefix,
		"FAKE_BREW_STATE="+filepath.Join(root, "fake-brew-state"),
		"FAKE_BREW_PLATFORM="+target,
		"FAKE_BREW_LOG="+filepath.Join(root, "brew.log"),
		"FAKE_BREW_TAMPER=1")
	if err == nil || !strings.Contains(output, "does not match the release manifest") {
		t.Fatalf("hash mismatch was accepted: %s: %v", output, err)
	}
}

func TestHomebrewInstallScriptOmitsUpgradeSuccessWithoutPreviousRelease(t *testing.T) {
	root := homebrewFixture(t)
	releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.3", "1.0.13", "1.0.83")
	helper := writeHomebrewReleaseHelper(t, root)
	prefix := filepath.Join(root, "fake-prefix")
	writeFakeHomebrew(t, root, prefix)
	target := homebrewHostTarget(t)
	output, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releaseDir, "--manifest", manifestPath},
		"SODAPOP_RELEASECTL="+helper,
		"SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1",
		"FAKE_BREW_PREFIX="+prefix,
		"FAKE_BREW_STATE="+filepath.Join(root, "fake-brew-state"),
		"FAKE_BREW_PLATFORM="+target,
		"FAKE_BREW_LOG="+filepath.Join(root, "brew.log"))
	if err != nil || !strings.Contains(output, "Homebrew install, reinstall, and uninstall checks passed") || strings.Contains(output, "upgrade") {
		t.Fatalf("single-release success output was wrong: %s: %v", output, err)
	}
}

func TestHomebrewInstallScriptPreservesPreexistingCollisionTargetsOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setup    func(t *testing.T, prefix string) string
		want     string
		pathKind string
	}{
		{
			name: "regular file",
			setup: func(t *testing.T, prefix string) string {
				t.Helper()
				path := filepath.Join(prefix, "bin", "sodapop")
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("keep regular file"), 0755); err != nil {
					t.Fatal(err)
				}
				return path
			},
			want:     "keep regular file",
			pathKind: "file",
		},
		{
			name: "dangling symlink",
			setup: func(t *testing.T, prefix string) string {
				t.Helper()
				path := filepath.Join(prefix, "bin", "sodapop")
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(prefix, "missing-target"), path); err != nil {
					t.Fatal(err)
				}
				return path
			},
			want:     filepath.Join("fake-prefix", "missing-target"),
			pathKind: "symlink",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := homebrewFixture(t)
			releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.3", "1.0.13", "1.0.83")
			helper := writeHomebrewReleaseHelper(t, root)
			prefix := filepath.Join(root, "fake-prefix")
			writeFakeHomebrew(t, root, prefix)
			path := tc.setup(t, prefix)
			target := homebrewHostTarget(t)
			output, err := homebrewRunScriptFixture(t, root,
				[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releaseDir, "--manifest", manifestPath},
				"SODAPOP_RELEASECTL="+helper,
				"SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1",
				"FAKE_BREW_PREFIX="+prefix,
				"FAKE_BREW_STATE="+filepath.Join(root, "fake-brew-state"),
				"FAKE_BREW_PLATFORM="+target,
				"FAKE_BREW_LOG="+filepath.Join(root, "brew.log"))
			if err == nil || !strings.Contains(output, "already contains") {
				t.Fatalf("collision guard did not fire: %s: %v", output, err)
			}
			info, statErr := os.Lstat(path)
			if statErr != nil {
				t.Fatalf("preexisting collision target was removed: %v", statErr)
			}
			switch tc.pathKind {
			case "file":
				data, readErr := os.ReadFile(path)
				if readErr != nil || string(data) != tc.want || !info.Mode().IsRegular() {
					t.Fatalf("regular collision target changed: %q %v mode=%v", data, readErr, info.Mode())
				}
			case "symlink":
				link, readErr := os.Readlink(path)
				if readErr != nil || link != filepath.Join(prefix, "missing-target") || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("dangling symlink collision target changed: %q %v mode=%v", link, readErr, info.Mode())
				}
			}
		})
	}
}

func TestHomebrewInstallScriptDoesNotDeleteSentinelOnLinkFailure(t *testing.T) {
	root := homebrewFixture(t)
	releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.3", "1.0.13", "1.0.83")
	helper := writeHomebrewReleaseHelper(t, root)
	prefix := filepath.Join(root, "fake-prefix")
	writeFakeHomebrew(t, root, prefix)
	target := homebrewHostTarget(t)
	output, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releaseDir, "--manifest", manifestPath},
		"SODAPOP_RELEASECTL="+helper,
		"SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1",
		"FAKE_BREW_PREFIX="+prefix,
		"FAKE_BREW_STATE="+filepath.Join(root, "fake-brew-state"),
		"FAKE_BREW_PLATFORM="+target,
		"FAKE_BREW_LOG="+filepath.Join(root, "brew.log"),
		"FAKE_BREW_REPLACE_COLLISION_WITH_SYMLINK=1")
	if err == nil || (!strings.Contains(output, "replaced the sentinel") && !strings.Contains(output, "link failure")) {
		t.Fatalf("link failure was not detected: %s: %v", output, err)
	}
	linkPath := filepath.Join(prefix, "bin", "sodapop")
	info, statErr := os.Lstat(linkPath)
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link failure cleanup removed the replacement symlink: %v mode=%v", statErr, info.Mode())
	}
}

func TestHomebrewInstallScriptDoesNotDeleteInstalledCommandWhenUninstallFails(t *testing.T) {
	root := homebrewFixture(t)
	releaseDir, manifestPath, _ := writeHomebrewReleaseInputs(t, root, "release", "1.2.3", "1.0.13", "1.0.83")
	helper := writeHomebrewReleaseHelper(t, root)
	prefix := filepath.Join(root, "fake-prefix")
	writeFakeHomebrew(t, root, prefix)
	target := homebrewHostTarget(t)
	output, err := homebrewRunScriptFixture(t, root,
		[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releaseDir, "--manifest", manifestPath},
		"SODAPOP_RELEASECTL="+helper,
		"SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1",
		"FAKE_BREW_PREFIX="+prefix,
		"FAKE_BREW_STATE="+filepath.Join(root, "fake-brew-state"),
		"FAKE_BREW_PLATFORM="+target,
		"FAKE_BREW_LOG="+filepath.Join(root, "brew.log"),
		"FAKE_BREW_FAIL_UNINSTALL=1")
	if err == nil || !strings.Contains(output, "uninstall failure") {
		t.Fatalf("uninstall failure was not surfaced: %s: %v", output, err)
	}
	info, statErr := os.Lstat(filepath.Join(prefix, "bin", "sodapop"))
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("installed command path was deleted on uninstall failure: %v mode=%v", statErr, info.Mode())
	}
}
