// Package distribution validates release bytes. A manifest binds hashes; it is
// not authentication. Trusted transport and release attestations are separate.
package distribution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Manifest struct {
	SchemaVersion         int        `json:"schema_version"`
	Version               string     `json:"version"`
	Commit                string     `json:"commit"`
	CopilotRuntimeVersion string     `json:"copilot_runtime_version"`
	CopilotSDKVersion     string     `json:"copilot_sdk_version"`
	Artifacts             []Artifact `json:"artifacts"`
}

type Artifact struct {
	Platform      string `json:"platform"`
	Archive       string `json:"archive"`
	ArchiveSHA256 string `json:"archive_sha256"`
	BinarySHA256  string `json:"binary_sha256"`
}

var (
	defaultPlatforms = []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"}
	versionPattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	commitPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func DefaultPlatforms() []string { return slices.Clone(defaultPlatforms) }

func ValidateVersion(version string) error {
	if len(version) > 128 || !versionPattern.MatchString(version) {
		return fmt.Errorf("invalid release version %q: expected SemVer without a v prefix", version)
	}
	core, _, _ := strings.Cut(version, "+")
	_, pre, _ := strings.Cut(core, "-")
	for _, part := range strings.Split(pre, ".") {
		if len(part) > 1 && part[0] == '0' && strings.Trim(part, "0123456789") == "" {
			return fmt.Errorf("invalid numeric prerelease identifier %q", part)
		}
	}
	return nil
}

func ParsePlatforms(value string) ([]string, error) {
	if value == "" {
		return nil, fmt.Errorf("platforms must not be empty")
	}
	platforms := strings.Split(value, ",")
	seen := make(map[string]bool)
	for _, platform := range platforms {
		if ValidatePlatform(platform) != nil || seen[platform] {
			return nil, fmt.Errorf("invalid or duplicate platform %q", platform)
		}
		seen[platform] = true
	}
	slices.Sort(platforms)
	return platforms, nil
}

func ValidatePlatform(platform string) error {
	if !slices.Contains(defaultPlatforms, platform) {
		return fmt.Errorf("invalid platform %q", platform)
	}
	return nil
}

func PackageName(version, platform string) string {
	return "sodapop-" + version + "-" + strings.ReplaceAll(platform, "/", "-")
}

func ArchiveName(version, platform string) string {
	suffix := ".tar.gz"
	if strings.HasPrefix(platform, "windows/") {
		suffix = ".zip"
	}
	return PackageName(version, platform) + suffix
}

func BinaryName(platform string) string {
	if strings.HasPrefix(platform, "windows/") {
		return "sodapop.exe"
	}
	return "sodapop"
}

// ValidateManifest always validates every record, even when only one platform
// will be downloaded. A non-nil expected set additionally requires completeness.
func ValidateManifest(m Manifest, expected []string) error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported manifest schema_version %d", m.SchemaVersion)
	}
	for _, version := range []string{m.Version, m.CopilotRuntimeVersion, m.CopilotSDKVersion} {
		if err := ValidateVersion(version); err != nil {
			return err
		}
	}
	if !commitPattern.MatchString(m.Commit) {
		return fmt.Errorf("commit must be a lowercase full 40-character Git commit")
	}
	if len(m.Artifacts) == 0 || len(m.Artifacts) > len(defaultPlatforms) {
		return fmt.Errorf("manifest must contain between one and six artifacts")
	}
	var platforms []string
	for _, artifact := range m.Artifacts {
		platforms = append(platforms, artifact.Platform)
		if artifact.Archive != ArchiveName(m.Version, artifact.Platform) {
			return fmt.Errorf("archive %q does not match version/platform", artifact.Archive)
		}
		if !digestPattern.MatchString(artifact.ArchiveSHA256) || !digestPattern.MatchString(artifact.BinarySHA256) {
			return fmt.Errorf("invalid SHA-256 for %s", artifact.Platform)
		}
	}
	got, err := ParsePlatforms(strings.Join(platforms, ","))
	if err != nil {
		return err
	}
	if expected != nil {
		want, err := ParsePlatforms(strings.Join(expected, ","))
		if err != nil {
			return err
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("incomplete release platforms: got %v, want %v", got, want)
		}
	}
	return nil
}

func ReadManifest(name string) (Manifest, error) {
	var m Manifest
	data, err := readRegular(name, 64<<10)
	if err != nil {
		return m, err
	}
	// encoding/json accepts duplicate and case-insensitive keys. Neither is an
	// unambiguous release contract, so reject them before typed decoding.
	keys := json.NewDecoder(bytes.NewReader(data))
	if err := strictJSON(keys, 0); err != nil {
		return m, fmt.Errorf("invalid manifest JSON: %w", err)
	}
	if _, err := keys.Token(); err != io.EOF {
		return m, fmt.Errorf("manifest contains trailing data")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, fmt.Errorf("decode manifest: %w", err)
	}
	if err := ValidateManifest(m, nil); err != nil {
		return m, err
	}
	return m, nil
}

func strictJSON(decoder *json.Decoder, depth int) error {
	if depth > 8 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	value, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := value.(json.Delim)
	if !ok {
		if value == nil {
			return fmt.Errorf("null is not allowed")
		}
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || name != strings.ToLower(name) || seen[name] {
				return fmt.Errorf("invalid or duplicate JSON key %q", key)
			}
			seen[name] = true
			if err := strictJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := strictJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delim)
	}
	_, err = decoder.Token()
	return err
}

// ReadPins parses only small source files, never importing the embedded bundle.
func ReadPins(repo string) (runtimeVersion, sdkVersion string, err error) {
	source, err := readRegular(filepath.Join(repo, "internal/runtimebundle/version.go"), 64<<10)
	if err != nil {
		return "", "", err
	}
	file, err := parser.ParseFile(token.NewFileSet(), "version.go", source, 0)
	if err != nil {
		return "", "", err
	}
	pins := make(map[string]string)
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value := spec.(*ast.ValueSpec)
			for i, name := range value.Names {
				if name.Name != "Version" && name.Name != "SDKVersion" {
					continue
				}
				if i >= len(value.Values) || pins[name.Name] != "" {
					return "", "", fmt.Errorf("invalid pin declaration %s", name.Name)
				}
				literal, ok := value.Values[i].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return "", "", fmt.Errorf("pin %s must be a string literal", name.Name)
				}
				pins[name.Name], err = strconv.Unquote(literal.Value)
				if err != nil {
					return "", "", err
				}
			}
		}
	}
	runtimeVersion, sdkVersion = pins["Version"], pins["SDKVersion"]
	for _, value := range []string{runtimeVersion, sdkVersion} {
		if err := ValidateVersion(value); err != nil {
			return "", "", fmt.Errorf("invalid pinned version: %w", err)
		}
	}
	mod, err := readRegular(filepath.Join(repo, "go.mod"), 1<<20)
	if err != nil {
		return "", "", err
	}
	matches := 0
	for _, line := range strings.Split(string(mod), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) > 0 && fields[0] == "require" {
			fields = fields[1:]
		}
		if len(fields) >= 2 && fields[0] == "github.com/github/copilot-sdk/go" && fields[1] == "v"+sdkVersion {
			matches++
		}
	}
	if matches != 1 {
		return "", "", fmt.Errorf("go.mod does not match the pinned Copilot SDK version")
	}
	return runtimeVersion, sdkVersion, nil
}

func Generate(dir, version, commit, repo string, platforms []string) (Manifest, error) {
	m := Manifest{SchemaVersion: 1, Version: version, Commit: strings.ToLower(commit)}
	if err := ValidateVersion(version); err != nil {
		return m, err
	}
	if !commitPattern.MatchString(m.Commit) {
		return m, fmt.Errorf("commit must be a full 40-character Git commit")
	}
	expected, err := ParsePlatforms(strings.Join(platforms, ","))
	if err != nil {
		return m, err
	}
	m.CopilotRuntimeVersion, m.CopilotSDKVersion, err = ReadPins(repo)
	if err != nil {
		return m, err
	}
	for _, platform := range expected {
		artifact := Artifact{Platform: platform, Archive: ArchiveName(version, platform)}
		artifact.ArchiveSHA256, artifact.BinarySHA256, err = inspectArchive(dir, version, artifact, nil)
		if err != nil {
			return m, fmt.Errorf("%s: %w", platform, err)
		}
		m.Artifacts = append(m.Artifacts, artifact)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	err = writeExclusive(filepath.Join(dir, "sodapop-"+version+"-manifest.json"), append(data, '\n'), 0644)
	return m, err
}

func readRegular(name string, limit int64) ([]byte, error) {
	file, err := openRegular(name, limit)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("%s exceeds size limit", name)
	}
	return data, err
}

func openRegular(name string, limit int64) (*os.File, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		return nil, fmt.Errorf("%s must be a nonempty regular file within size limit", name)
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		file.Close()
		return nil, fmt.Errorf("%s changed while opening", name)
	}
	return file, nil
}

func writeExclusive(name string, data []byte, mode os.FileMode) (err error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(name)
		}
	}()
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}
