package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/distribution"
)

type distributionManifest = distribution.Manifest

type distributionArtifact = distribution.Artifact

type distributionChannelReference struct {
	Channel       string
	Platform      string
	Archive       string
	ArchiveSHA256 string
	BinarySHA256  string
}

type distributionArchiveEntry struct {
	Mode    fs.FileMode
	Content []byte
}

type distributionTreeEntry struct {
	Mode   fs.FileMode
	Digest string
	Link   string
}

var (
	distributionCommitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	distributionDigestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	distributionRuntimePattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	distributionVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)
)

func readDistributionManifest(name string) (distributionManifest, error) {
	return distribution.ReadManifest(name)
}

func validateDistributionManifest(manifest distributionManifest, platforms []string) error {
	return distribution.ValidateManifest(manifest, platforms)
}

func validateDistributionAssets(root string, manifest distributionManifest) error {
	return distribution.Verify(root, manifest, "", nil)
}

func validateDistributionArchivePayload(
	archivePath, packageRoot string,
	required []string,
	allowedPrefixes []string,
) error {
	entries, err := readDistributionArchive(archivePath)
	if err != nil {
		return err
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		requiredSet[path.Join(packageRoot, name)] = struct{}{}
	}
	for name := range entries {
		if _, ok := requiredSet[name]; ok {
			delete(requiredSet, name)
			continue
		}
		allowed := false
		for _, prefix := range allowedPrefixes {
			fullPrefix := strings.TrimSuffix(path.Join(packageRoot, prefix), "/") + "/"
			if strings.HasPrefix(name, fullPrefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("archive contains unexpected file %s", name)
		}
	}
	if len(requiredSet) != 0 {
		missing := make([]string, 0, len(requiredSet))
		for name := range requiredSet {
			missing = append(missing, name)
		}
		slices.Sort(missing)
		return fmt.Errorf("archive is missing required files: %v", missing)
	}
	return nil
}

func readDistributionArchive(name string) (map[string]distributionArchiveEntry, error) {
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		return readDistributionTarGzip(name)
	case strings.HasSuffix(name, ".zip"):
		return readDistributionZip(name)
	default:
		return nil, fmt.Errorf("unsupported distribution archive %s", name)
	}
}

func readDistributionTarGzip(name string) (map[string]distributionArchiveEntry, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	entries := make(map[string]distributionArchiveEntry)
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		clean, err := cleanDistributionArchivePath(header.Name)
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("archive entry %s is not a regular file", clean)
		}
		if _, found := entries[clean]; found {
			return nil, fmt.Errorf("archive contains duplicate entry %s", clean)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		entries[clean] = distributionArchiveEntry{Mode: fs.FileMode(header.Mode), Content: content}
	}
	return entries, nil
}

func readDistributionZip(name string) (map[string]distributionArchiveEntry, error) {
	reader, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	entries := make(map[string]distributionArchiveEntry)
	for _, file := range reader.File {
		clean, err := cleanDistributionArchivePath(file.Name)
		if err != nil {
			return nil, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if !file.Mode().IsRegular() {
			return nil, fmt.Errorf("archive entry %s is not a regular file", clean)
		}
		if _, found := entries[clean]; found {
			return nil, fmt.Errorf("archive contains duplicate entry %s", clean)
		}
		stream, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		entries[clean] = distributionArchiveEntry{Mode: file.Mode(), Content: content}
	}
	return entries, nil
}

func cleanDistributionArchivePath(name string) (string, error) {
	if name == "" || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe archive entry %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != strings.TrimSuffix(name, "/") {
		return "", fmt.Errorf("unsafe archive entry %q", name)
	}
	return clean, nil
}

func requireRegularDistributionFile(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	return nil
}

func distributionSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parseDistributionChecksum(data []byte) (string, string, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' ||
		strings.Contains(strings.TrimSuffix(string(data), "\n"), "\n") {
		return "", "", fmt.Errorf("checksum must contain exactly one newline-terminated entry")
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || !distributionDigestPattern.MatchString(fields[0]) ||
		path.Base(fields[1]) != fields[1] {
		return "", "", fmt.Errorf("invalid checksum entry")
	}
	return fields[0], fields[1], nil
}

func runInstalledCommandSmoke(executable, version, home string) (map[string]string, error) {
	results := make(map[string]string, 3)
	for _, argument := range []string{"--help", "--version", "--check-runtime"} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(ctx, executable, argument)
		command.Env = credentialFreeDistributionEnvironment(home)
		output, err := command.CombinedOutput()
		contextErr := ctx.Err()
		cancel()
		if contextErr == context.DeadlineExceeded {
			return nil, fmt.Errorf("%s %s timed out", executable, argument)
		}
		if err != nil {
			return nil, fmt.Errorf("%s %s failed: %s: %w", executable, argument, output, err)
		}
		if len(bytes.TrimSpace(output)) == 0 {
			return nil, fmt.Errorf("%s %s produced no output", executable, argument)
		}
		results[argument] = string(output)
	}
	if !strings.Contains(results["--version"], version) {
		return nil, fmt.Errorf("installed command version output %q does not contain %q", results["--version"], version)
	}
	return results, nil
}

func credentialFreeDistributionEnvironment(home string) []string {
	environment := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "SODAPOP_") ||
			strings.HasPrefix(upper, "COPILOT_") ||
			strings.HasPrefix(upper, "GITHUB_") ||
			strings.HasPrefix(upper, "GH_") {
			continue
		}
		switch upper {
		case "HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA":
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"APPDATA="+filepath.Join(home, "appdata"),
		"LOCALAPPDATA="+filepath.Join(home, "local-appdata"),
	)
	return environment
}

func snapshotDistributionTree(root string) (map[string]distributionTreeEntry, error) {
	snapshot := make(map[string]distributionTreeEntry)
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		item := distributionTreeEntry{Mode: info.Mode()}
		if info.Mode()&fs.ModeSymlink != 0 {
			item.Link, err = os.Readlink(name)
		} else if info.Mode().IsRegular() {
			var data []byte
			data, err = os.ReadFile(name)
			item.Digest = distributionSHA256(data)
		} else if info.IsDir() {
			// Directory ownership matters during uninstall even when it is empty.
		} else {
			err = fmt.Errorf("%s is not a regular file, directory, or symlink", relative)
		}
		if err != nil {
			return err
		}
		snapshot[relative] = item
		return nil
	})
	return snapshot, err
}

func validateDistributionChanges(
	before, after map[string]distributionTreeEntry,
	ownedPaths []string,
) error {
	paths := make(map[string]struct{}, len(before)+len(after))
	for name := range before {
		paths[name] = struct{}{}
	}
	for name := range after {
		paths[name] = struct{}{}
	}
	for name := range paths {
		if before[name] == after[name] {
			continue
		}
		if !distributionPathOwned(name, ownedPaths) {
			return fmt.Errorf("distribution operation changed unowned path %s", name)
		}
	}
	return nil
}

func validateDistributionUninstall(
	before, after map[string]distributionTreeEntry,
	ownedPaths []string,
) error {
	if err := validateDistributionChanges(before, after, ownedPaths); err != nil {
		return err
	}
	for name := range before {
		if distributionPathOwned(name, ownedPaths) {
			if _, found := after[name]; found {
				return fmt.Errorf("uninstall left package-owned path %s", name)
			}
		}
	}
	return nil
}

func validateFailedDistributionUpgrade(
	before, after map[string]distributionTreeEntry,
	commandPath string,
) error {
	if !distributionTreesEqual(before, after) {
		return fmt.Errorf("failed upgrade changed the installation tree")
	}
	if _, found := after[commandPath]; !found {
		return fmt.Errorf("failed upgrade removed installed command %s", commandPath)
	}
	return nil
}

func distributionPathOwned(name string, ownedPaths []string) bool {
	name = strings.Trim(path.Clean(filepath.ToSlash(name)), "/")
	for _, owned := range ownedPaths {
		owned = strings.Trim(path.Clean(filepath.ToSlash(owned)), "/")
		if name == owned || strings.HasPrefix(name, owned+"/") {
			return true
		}
	}
	return false
}

func distributionTreesEqual(left, right map[string]distributionTreeEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for name, entry := range left {
		if right[name] != entry {
			return false
		}
	}
	return true
}

func validateDistributionChannelHashes(
	manifest distributionManifest,
	references []distributionChannelReference,
) error {
	artifacts := make(map[string]distributionArtifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifacts[artifact.Platform] = artifact
	}
	for _, reference := range references {
		artifact, found := artifacts[reference.Platform]
		if !found {
			return fmt.Errorf("%s references unknown platform %s", reference.Channel, reference.Platform)
		}
		if reference.Archive != "" && reference.Archive != artifact.Archive {
			return fmt.Errorf("%s archive = %s, want %s", reference.Channel, reference.Archive, artifact.Archive)
		}
		if reference.ArchiveSHA256 != "" && reference.ArchiveSHA256 != artifact.ArchiveSHA256 {
			return fmt.Errorf("%s archive SHA-256 differs from the release manifest", reference.Channel)
		}
		if reference.BinarySHA256 != "" && reference.BinarySHA256 != artifact.BinarySHA256 {
			return fmt.Errorf("%s binary SHA-256 differs from the release manifest", reference.Channel)
		}
		if reference.ArchiveSHA256 == "" && reference.BinarySHA256 == "" {
			return fmt.Errorf("%s supplies no release hash", reference.Channel)
		}
	}
	return nil
}

func TestDistributionContractReleaseAssetsAndPackageContents(t *testing.T) {
	root := t.TempDir()
	version := "1.2.3"
	platform := "linux/amd64"
	packageRoot := "sodapop-" + version + "-linux-amd64"
	archive := packageRoot + ".tar.gz"
	executable := []byte("#!/bin/sh\nprintf 'sodapop fixture\\n'\n")
	writeDistributionTarGzip(t, filepath.Join(root, archive), map[string]distributionArchiveEntry{
		packageRoot + "/sodapop":                          {Mode: 0755, Content: executable},
		packageRoot + "/README.md":                        {Mode: 0644, Content: []byte("fixture README")},
		packageRoot + "/LICENSE":                          {Mode: 0644, Content: []byte("fixture license")},
		packageRoot + "/THIRD_PARTY_NOTICES.md":           {Mode: 0644, Content: []byte("fixture notices")},
		packageRoot + "/LICENSES/copilot-runtime.license": {Mode: 0644, Content: []byte("runtime terms")},
		packageRoot + "/LICENSES/dependency.license":      {Mode: 0644, Content: []byte("dependency terms")},
	})
	archiveData, err := os.ReadFile(filepath.Join(root, archive))
	if err != nil {
		t.Fatal(err)
	}
	archiveDigest := distributionSHA256(archiveData)
	writeFixtureFile(t, root, archive+".sha256", archiveDigest+"  "+archive+"\n", 0600)
	manifest := distributionManifest{
		SchemaVersion:         1,
		Version:               version,
		Commit:                strings.Repeat("a", 40),
		CopilotRuntimeVersion: "1.0.83",
		CopilotSDKVersion:     "1.0.13",
		Artifacts: []distributionArtifact{{
			Platform:      platform,
			Archive:       archive,
			ArchiveSHA256: archiveDigest,
			BinarySHA256:  distributionSHA256(executable),
		}},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "manifest.json", string(manifestData)+"\n", 0600)
	loaded, err := readDistributionManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDistributionManifest(loaded, []string{platform}); err != nil {
		t.Fatal(err)
	}
	if err := validateDistributionAssets(root, loaded); err != nil {
		t.Fatal(err)
	}
	if err := validateDistributionArchivePayload(filepath.Join(root, archive), packageRoot,
		[]string{"sodapop", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"},
		[]string{"LICENSES"}); err != nil {
		t.Fatal(err)
	}

	loaded.Artifacts[0].BinarySHA256 = strings.Repeat("0", 64)
	if err := validateDistributionAssets(root, loaded); err == nil ||
		!strings.Contains(err.Error(), "binary SHA-256") {
		t.Fatalf("binary hash mismatch was accepted: %v", err)
	}
	loaded.Artifacts[0].BinarySHA256 = distributionSHA256(executable)
	writeFixtureFile(t, root, archive+".sha256",
		archiveDigest+"  "+archive+"\n"+archiveDigest+"  "+archive+"\n", 0600)
	if err := validateDistributionAssets(root, loaded); err == nil ||
		!strings.Contains(err.Error(), "invalid checksum file") {
		t.Fatalf("multi-entry checksum was accepted: %v", err)
	}
}

func TestDistributionContractRejectsUnsafeOrUnexpectedPackageContents(t *testing.T) {
	for _, unexpected := range append([]string{".env"}, nativeWebsiteIsolationFiles()...) {
		t.Run("unexpected/"+unexpected, func(t *testing.T) {
			root := t.TempDir()
			packageRoot := "sodapop-v1.2.3-linux-amd64"
			archive := filepath.Join(root, packageRoot+".tar.gz")
			writeDistributionTarGzip(t, archive, map[string]distributionArchiveEntry{
				packageRoot + "/sodapop":       {Mode: 0755, Content: []byte("fixture")},
				packageRoot + "/" + unexpected: {Mode: 0600, Content: []byte("FIXTURE_ONLY_MUST_NOT_SHIP")},
				packageRoot + "/README.md":     {Mode: 0644, Content: []byte("fixture")},
			})
			err := validateDistributionArchivePayload(archive, packageRoot,
				[]string{"sodapop", "README.md"}, nil)
			if err == nil || !strings.Contains(err.Error(), "unexpected file "+packageRoot+"/"+unexpected) {
				t.Fatalf("unrelated package content %s was accepted: %v", unexpected, err)
			}
		})
	}

	t.Run("path traversal", func(t *testing.T) {
		root := t.TempDir()
		archive := filepath.Join(root, "unsafe.tar.gz")
		writeDistributionTarGzip(t, archive, map[string]distributionArchiveEntry{
			"package/../outside": {Mode: 0600, Content: []byte("escape")},
		})
		if _, err := readDistributionArchive(archive); err == nil ||
			!strings.Contains(err.Error(), "unsafe archive entry") {
			t.Fatalf("path traversal was accepted: %v", err)
		}
	})
}

func TestDistributionContractSupportsWindowsZipAssets(t *testing.T) {
	hashes := make(map[string]string)
	for _, platform := range []string{"windows/amd64", "windows/arm64"} {
		t.Run(platform, func(t *testing.T) {
			root := t.TempDir()
			version := "1.2.3"
			packageRoot := distribution.PackageName(version, platform)
			archive := distribution.ArchiveName(version, platform)
			executable := fixtureBinary(platform)
			writeDistributionZip(t, filepath.Join(root, archive), map[string]distributionArchiveEntry{
				packageRoot + "/sodapop.exe":                      {Mode: 0755, Content: executable},
				packageRoot + "/README.md":                        {Mode: 0644, Content: []byte("fixture README")},
				packageRoot + "/LICENSE":                          {Mode: 0644, Content: []byte("fixture license")},
				packageRoot + "/THIRD_PARTY_NOTICES.md":           {Mode: 0644, Content: []byte("fixture notices")},
				packageRoot + "/LICENSES/copilot-runtime.license": {Mode: 0644, Content: []byte("runtime terms")},
				packageRoot + "/LICENSES/dependency.license":      {Mode: 0644, Content: []byte("dependency terms")},
			})
			data, err := os.ReadFile(filepath.Join(root, archive))
			if err != nil {
				t.Fatal(err)
			}
			archiveDigest := distributionSHA256(data)
			writeFixtureFile(t, root, archive+".sha256", archiveDigest+"  "+archive+"\n", 0600)
			hashes[platform] = distributionSHA256(executable)
			manifest := distributionManifest{
				SchemaVersion:         1,
				Version:               version,
				Commit:                strings.Repeat("b", 40),
				CopilotRuntimeVersion: "1.0.83",
				CopilotSDKVersion:     "1.0.13",
				Artifacts: []distributionArtifact{{
					Platform:      platform,
					Archive:       archive,
					ArchiveSHA256: archiveDigest,
					BinarySHA256:  hashes[platform],
				}},
			}
			if err := validateDistributionManifest(manifest, []string{platform}); err != nil {
				t.Fatal(err)
			}
			if err := validateDistributionAssets(root, manifest); err != nil {
				t.Fatal(err)
			}
			if err := validateDistributionArchivePayload(filepath.Join(root, archive), packageRoot,
				[]string{"sodapop.exe", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"}, []string{"LICENSES"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if hashes["windows/amd64"] == hashes["windows/arm64"] {
		t.Fatal("Windows architecture fixtures must have distinct binary hashes")
	}
}

func TestDistributionContractInstalledCommandSmokeIsCredentialFree(t *testing.T) {
	executable := buildDistributionSmokeFixture(t)
	home := filepath.Join(t.TempDir(), "isolated home")
	t.Setenv("SODAPOP_GITHUB_CLIENT_ID", "must-not-leak")
	t.Setenv("GITHUB_TOKEN", "must-not-leak")
	t.Setenv("COPILOT_TOKEN", "must-not-leak")
	results, err := runInstalledCommandSmoke(executable, "v1.2.3", home)
	if err != nil {
		t.Fatal(err)
	}
	for _, argument := range []string{"--help", "--version", "--check-runtime"} {
		if !strings.Contains(results[argument], argument) {
			t.Errorf("%s did not exercise the installed command: %q", argument, results[argument])
		}
	}
}

func TestDistributionContractUpgradeAndUninstallBoundaries(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "bin/sodapop", "version one", 0755)
	writeFixtureFile(t, root, "share/sodapop/package.json", "owned metadata", 0600)
	writeFixtureFile(t, root, "config/preferences.json", "user preferences", 0600)
	writeFixtureFile(t, root, "state/session.json", "user session", 0600)
	if err := os.MkdirAll(filepath.Join(root, "cache", "runtime"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "home/.zshrc", "shell profile", 0600)
	writeFixtureFile(t, root, "bin/unrelated", "keep", 0755)
	owned := []string{"bin/sodapop", "share/sodapop"}

	beforeUpgrade, err := snapshotDistributionTree(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "bin/sodapop", "version two", 0755)
	writeFixtureFile(t, root, "share/sodapop/package.json", "new owned metadata", 0600)
	afterUpgrade, err := snapshotDistributionTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDistributionChanges(beforeUpgrade, afterUpgrade, owned); err != nil {
		t.Fatal(err)
	}
	if err := validateFailedDistributionUpgrade(afterUpgrade, mapsCloneDistributionTree(afterUpgrade), "bin/sodapop"); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(root, "bin", "sodapop")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "share", "sodapop")); err != nil {
		t.Fatal(err)
	}
	afterUninstall, err := snapshotDistributionTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDistributionUninstall(afterUpgrade, afterUninstall, owned); err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{
		"config/preferences.json", "state/session.json", "cache/runtime", "home/.zshrc", "bin/unrelated",
	} {
		if afterUninstall[preserved] != afterUpgrade[preserved] {
			t.Errorf("uninstall changed preserved path %s", preserved)
		}
	}

	mutated := mapsCloneDistributionTree(afterUpgrade)
	delete(mutated, "config/preferences.json")
	if err := validateDistributionChanges(afterUpgrade, mutated, owned); err == nil ||
		!strings.Contains(err.Error(), "unowned path") {
		t.Fatalf("unowned state removal was accepted: %v", err)
	}
	mutated = mapsCloneDistributionTree(afterUpgrade)
	delete(mutated, "bin/sodapop")
	if err := validateFailedDistributionUpgrade(afterUpgrade, mutated, "bin/sodapop"); err == nil ||
		!strings.Contains(err.Error(), "changed the installation tree") {
		t.Fatalf("destructive failed upgrade was accepted: %v", err)
	}
}

func TestDistributionContractCrossChannelHashes(t *testing.T) {
	archiveDigest := strings.Repeat("a", 64)
	binaryDigest := strings.Repeat("b", 64)
	manifest := distributionManifest{Artifacts: []distributionArtifact{{
		Platform:      "darwin/arm64",
		Archive:       "sodapop-v1.2.3-darwin-arm64.tar.gz",
		ArchiveSHA256: archiveDigest,
		BinarySHA256:  binaryDigest,
	}}}
	references := []distributionChannelReference{
		{Channel: "homebrew", Platform: "darwin/arm64", ArchiveSHA256: archiveDigest},
		{
			Channel: "npm", Platform: "darwin/arm64",
			Archive: "sodapop-v1.2.3-darwin-arm64.tar.gz", ArchiveSHA256: archiveDigest, BinarySHA256: binaryDigest,
		},
		{Channel: "direct", Platform: "darwin/arm64", BinarySHA256: binaryDigest},
	}
	if err := validateDistributionChannelHashes(manifest, references); err != nil {
		t.Fatal(err)
	}
	references[1].BinarySHA256 = strings.Repeat("c", 64)
	if err := validateDistributionChannelHashes(manifest, references); err == nil ||
		!strings.Contains(err.Error(), "npm binary SHA-256") {
		t.Fatalf("cross-channel hash mismatch was accepted: %v", err)
	}
}

func TestDistributionContractGeneratedChannelsUseReleaseHashes(t *testing.T) {
	version := "1.2.3"
	releaseRoot := t.TempDir()
	platforms := []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"}
	manifest := distributionManifest{
		SchemaVersion:         1,
		Version:               version,
		Commit:                strings.Repeat("d", 40),
		CopilotRuntimeVersion: "1.0.83",
		CopilotSDKVersion:     "1.0.13",
	}
	for _, platform := range platforms {
		goos, goarch, _ := strings.Cut(platform, "/")
		packageRoot := "sodapop-" + version + "-" + goos + "-" + goarch
		archive := packageRoot + ".tar.gz"
		executable := []byte("fixture binary for " + platform)
		writeDistributionTarGzip(t, filepath.Join(releaseRoot, archive), map[string]distributionArchiveEntry{
			packageRoot + "/sodapop":                          {Mode: 0755, Content: executable},
			packageRoot + "/LICENSE":                          {Mode: 0644, Content: []byte("fixture license")},
			packageRoot + "/THIRD_PARTY_NOTICES.md":           {Mode: 0644, Content: []byte("fixture notices")},
			packageRoot + "/LICENSES/copilot-runtime.license": {Mode: 0644, Content: []byte("runtime terms")},
			packageRoot + "/LICENSES/dependency.license":      {Mode: 0644, Content: []byte("dependency terms")},
		})
		archiveData, err := os.ReadFile(filepath.Join(releaseRoot, archive))
		if err != nil {
			t.Fatal(err)
		}
		archiveDigest := distributionSHA256(archiveData)
		writeFixtureFile(t, releaseRoot, archive+".sha256", archiveDigest+"  "+archive+"\n", 0600)
		manifest.Artifacts = append(manifest.Artifacts, distributionArtifact{
			Platform:      platform,
			Archive:       archive,
			ArchiveSHA256: archiveDigest,
			BinarySHA256:  distributionSHA256(executable),
		})
	}
	if err := validateDistributionManifest(manifest, platforms); err != nil {
		t.Fatal(err)
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(releaseRoot, "sodapop-"+version+"-manifest.json")
	writeFixtureFile(t, releaseRoot, filepath.Base(manifestPath), string(manifestData), 0644)

	formula := filepath.Join(t.TempDir(), "Formula", "sodapop.rb")
	homebrew := exec.Command("bash", "scripts/generate-homebrew-formula.sh", version, formula)
	homebrew.Dir = ".."
	homebrew.Env = append(os.Environ(), "SODAPOP_HOMEBREW_RELEASE_DIR="+releaseRoot,
		"SODAPOP_HOMEBREW_MANIFEST="+manifestPath, "SODAPOP_RELEASECTL="+releaseHelper(t))
	if output, err := homebrew.CombinedOutput(); err != nil {
		t.Fatalf("generate Homebrew fixture: %s: %v", output, err)
	}
	formulaData, err := os.ReadFile(formula)
	if err != nil {
		t.Fatal(err)
	}
	references, err := distributionHomebrewReferences(string(formulaData), manifest)
	if err != nil {
		t.Fatal(err)
	}

	node, err := exec.LookPath("node")
	if err == nil {
		npmParent, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		npmOutput := filepath.Join(npmParent, "npm-output")
		npm := exec.Command(node, "npm/scripts/build-packages.mjs",
			"--version", version, "--release-dir", releaseRoot, "--manifest", manifestPath,
			"--platforms", strings.Join(platforms, ","), "--output", npmOutput)
		npm.Dir = ".."
		npm.Env = append(os.Environ(), "SODAPOP_RELEASECTL="+releaseHelper(t))
		if output, runErr := npm.CombinedOutput(); runErr != nil {
			t.Fatalf("build npm fixture: %s: %v", output, runErr)
		}
		for _, artifact := range manifest.Artifacts {
			packageID := strings.ReplaceAll(artifact.Platform, "/", "-")
			metadataPath := filepath.Join(npmOutput, "platforms", packageID, "package.json")
			metadataData, readErr := os.ReadFile(metadataPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			var metadata struct {
				Sodapop struct {
					ReleaseArchive string `json:"releaseArchive"`
					SHA256         string `json:"sha256"`
				} `json:"sodapop"`
			}
			if err := json.Unmarshal(metadataData, &metadata); err != nil {
				t.Fatal(err)
			}
			binary, readErr := os.ReadFile(filepath.Join(npmOutput, "platforms", packageID, "bin", "sodapop"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			references = append(references, distributionChannelReference{
				Channel:       "npm/" + packageID,
				Platform:      artifact.Platform,
				Archive:       metadata.Sodapop.ReleaseArchive,
				ArchiveSHA256: metadata.Sodapop.SHA256,
				BinarySHA256:  distributionSHA256(binary),
			})
		}
	} else {
		t.Log("node is unavailable; npm's own credential-free tests cover package generation")
	}
	if err := validateDistributionChannelHashes(manifest, references); err != nil {
		t.Fatal(err)
	}
}

func TestDistributionContractPackageManagerBoundaries(t *testing.T) {
	type packageMetadata struct {
		Files   []string          `json:"files"`
		Scripts map[string]string `json:"scripts"`
	}
	metadataPaths, err := filepath.Glob("../npm/packages/*/package.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(metadataPaths) == 0 {
		t.Fatal("npm package metadata is missing")
	}
	for _, metadataPath := range metadataPaths {
		data, err := os.ReadFile(metadataPath)
		if err != nil {
			t.Fatal(err)
		}
		var metadata packageMetadata
		if err := json.Unmarshal(data, &metadata); err != nil {
			t.Fatal(err)
		}
		for _, hook := range []string{
			"preinstall", "install", "postinstall",
			"preuninstall", "uninstall", "postuninstall",
		} {
			if _, found := metadata.Scripts[hook]; found {
				t.Errorf("%s defines destructive lifecycle hook %s", metadataPath, hook)
			}
		}
		if len(metadata.Files) == 0 {
			t.Errorf("%s does not define an explicit package file allowlist", metadataPath)
		}
	}

	formula, err := os.ReadFile("../packaging/homebrew/Formula/sodapop.rb.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(formula))
	for _, forbidden := range []string{
		"def uninstall", "zap do", ".zshrc", ".bashrc", ".profile",
		"preferences", "session history", "credential",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("Homebrew formula owns user state through %q", forbidden)
		}
	}
	if strings.Count(text, `bin.install "sodapop"`) != 1 {
		t.Error("Homebrew formula must install only the Sodapop command")
	}
}

func TestDistributionContractDocumentation(t *testing.T) {
	data, err := os.ReadFile("../docs/distribution.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	for _, required := range []string{
		"## Release assets",
		"## Checksum verification",
		"## Installed-command smoke checks",
		"## Package contents",
		"## Upgrade and uninstall boundaries",
		"## Cross-channel hash consistency",
		"`archive_sha256`",
		"`binary_sha256`",
		"sodapop --help",
		"sodapop --version",
		"sodapop --check-runtime",
	} {
		if !strings.Contains(document, required) {
			t.Errorf("distribution contract is missing %q", required)
		}
	}
}

func distributionHomebrewReferences(
	formula string,
	manifest distributionManifest,
) ([]distributionChannelReference, error) {
	artifacts := make(map[string]distributionArtifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifacts[artifact.Archive] = artifact
	}
	pattern := regexp.MustCompile(`(?m)^[[:space:]]*url "[^"]+/([^"]+)"\n[[:space:]]*sha256 "([0-9a-f]{64})"$`)
	matches := pattern.FindAllStringSubmatch(formula, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("Homebrew formula has no release archive hashes")
	}
	references := make([]distributionChannelReference, 0, len(matches))
	for _, match := range matches {
		artifact, found := artifacts[match[1]]
		if !found {
			return nil, fmt.Errorf("Homebrew formula references unknown archive %s", match[1])
		}
		references = append(references, distributionChannelReference{
			Channel:       "homebrew/" + strings.ReplaceAll(artifact.Platform, "/", "-"),
			Platform:      artifact.Platform,
			Archive:       artifact.Archive,
			ArchiveSHA256: match[2],
		})
	}
	return references, nil
}

func writeDistributionTarGzip(
	t *testing.T,
	name string,
	entries map[string]distributionArchiveEntry,
) {
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

func writeDistributionZip(
	t *testing.T,
	name string,
	entries map[string]distributionArchiveEntry,
) {
	t.Helper()
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(entries))
	for entry := range entries {
		names = append(names, entry)
	}
	slices.Sort(names)
	for _, entry := range names {
		value := entries[entry]
		header := &zip.FileHeader{Name: entry, Method: zip.Deflate}
		header.SetMode(value.Mode)
		stream, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write(value.Content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func buildDistributionSmokeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "SODAPOP_") ||
			strings.HasPrefix(upper, "COPILOT_") ||
			strings.HasPrefix(upper, "GITHUB_") ||
			strings.HasPrefix(upper, "GH_") {
			fmt.Fprintln(os.Stderr, "credential environment leaked:", name)
			os.Exit(2)
		}
	}
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--help":
		fmt.Println("--help fixture")
	case "--version":
		fmt.Println("--version v1.2.3")
	case "--check-runtime":
		fmt.Println("--check-runtime fixture")
	default:
		os.Exit(2)
	}
}
`
	writeFixtureFile(t, root, "main.go", source, 0600)
	executable := filepath.Join(root, "sodapop")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.Command("go", "build", "-o", executable, "main.go")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build smoke fixture: %s: %v", output, err)
	}
	return executable
}

func mapsCloneDistributionTree(source map[string]distributionTreeEntry) map[string]distributionTreeEntry {
	clone := make(map[string]distributionTreeEntry, len(source))
	for name, entry := range source {
		clone[name] = entry
	}
	return clone
}
