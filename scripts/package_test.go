package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/distribution"
)

const packageVersion = "1.2.3"

func packageDocumentation() []string {
	files := []string{
		"LICENSE", "README.md", "THIRD_PARTY_NOTICES.md",
		"docs/authentication.md", "docs/architecture.md", "docs/live-qualification.md", "docs/vhs/README.md",
		"docs/branding.md", "images/sodapop-title.gif", "images/sodapop-title.png",
	}
	for _, clip := range demoClips {
		for _, extension := range []string{"gif", "png"} {
			files = append(files, "docs/assets/demos/"+clip+"."+extension)
		}
	}
	return files
}

func nativeWebsiteIsolationFiles() []string {
	return append(websiteIsolationFiles(),
		"site/AGENTS.md",
		"site/astro.config.mjs",
		"site/src/pages/index.astro",
		"site/src/content/docs/docs/getting-started.md",
		"site/node_modules/astro/package.json",
		"site/node_modules/@vendor/dependency/AGENTS.md",
		"site/dist/index.html",
		"site/dist/_astro/client.js",
		"site/.astro/content.d.ts",
		"site/.generated/release-catalog.json",
		"site/public/assets/mascot.png",
		"site/.env",
		"site/.env.local",
		"site/.sodapop.env",
		"site/.npmrc",
		"site/dist/.env",
		"site/.generated/.env.local",
		"node_modules/frontend-dependency/AGENTS.md",
		"dist/site/index.html",
	)
}

func packageFixture(t *testing.T) string {
	t.Helper()
	root := scriptFixture(t)
	for _, target := range distribution.DefaultPlatforms() {
		license := "internal/runtimebundle/zcopilot_1.0.83_" + strings.ReplaceAll(target, "/", "_") + ".license"
		if strings.HasPrefix(target, "windows/") {
			license = "internal/runtimebundle/zcopilot_1.0.83_" + strings.ReplaceAll(target, "/", "_") + ".exe.license"
		}
		writeFixtureFile(t, root, license, "runtime terms", 0600)
		stage := "dist/" + distribution.PackageName(packageVersion, target)
		for _, path := range []string{"unrelated-private-note", ".sodapop.env", "LICENSES/.sodapop.env", "LICENSES/unrelated-private-note"} {
			writeFixtureFile(t, root, stage+"/"+path, "STALE_STAGING_CONTENT_MUST_NOT_SHIP", 0600)
		}
	}
	for _, name := range packageDocumentation() {
		writeFixtureFile(t, root, name, "fixture "+name, 0600)
	}
	writeFixtureFile(t, root, "docs/assets/demos/unrelated-private-note", "PRIVATE_MEDIA_MUST_NOT_SHIP", 0600)
	for _, name := range []string{".env", ".env.local", ".sodapop.env", ".sodapop.env.local"} {
		writeFixtureFile(t, root, name, "PRIVATE_CONFIGURATION_MUST_NOT_SHIP", 0600)
	}
	for _, name := range nativeWebsiteIsolationFiles() {
		writeFixtureFile(t, root, name, "WEBSITE_CONTENT_MUST_NOT_SHIP "+name, 0600)
	}
	return root
}

func runPackage(t *testing.T, root string, env ...string) (string, error) {
	t.Helper()
	return runScriptFixture(t, root, []string{"bash", "scripts/package.sh"},
		append([]string{"SODAPOP_TARGET=linux/amd64", "SODAPOP_VERSION=" + packageVersion,
			"SODAPOP_GITHUB_CLIENT_ID=fixture.public-client", "SODAPOP_OUTPUT=must-not-be-used/sodapop"}, env...)...)
}

func packageBinary(target string) string { return distribution.BinaryName(target) }

func packageManifest(t *testing.T, root string, platforms []string) distribution.Manifest {
	t.Helper()
	env := []string{"SODAPOP_VERSION=" + packageVersion, "SODAPOP_COMMIT=" + strings.Repeat("a", 40)}
	if platforms != nil {
		env = append(env, "SODAPOP_PLATFORMS="+strings.Join(platforms, ","))
	}
	output, err := runScriptFixture(t, root, []string{"bash", "scripts/release-manifest.sh"}, env...)
	if err != nil || output != "Generated dist/sodapop-"+packageVersion+"-manifest.json\n" {
		t.Fatalf("manifest generation failed: %s: %v", output, err)
	}
	manifest, err := distribution.ReadManifest(filepath.Join(root, "dist", "sodapop-"+packageVersion+"-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestPackageIncludesLicensedDocumentationAndExcludesUnrelatedFiles(t *testing.T) {
	targets := distribution.DefaultPlatforms()
	if runtime.GOOS == "windows" {
		targets = []string{"windows/amd64", "windows/arm64"}
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			root := packageFixture(t)
			name := distribution.PackageName(packageVersion, target)
			archive := distribution.ArchiveName(packageVersion, target)
			output, err := runPackage(t, root, "SODAPOP_TARGET="+target)
			if err != nil || !strings.Contains(output, "Packaged dist/"+archive+"\n") {
				t.Fatalf("package failed: %s: %v", output, err)
			}
			manifest := packageManifest(t, root, []string{target})
			extracted := filepath.Join(t.TempDir(), "extracted")
			if err := distribution.Extract(filepath.Join(root, "dist"), manifest, target, extracted); err != nil {
				t.Fatal(err)
			}
			payload := filepath.Join(extracted, name)
			files := make(map[string]string)
			err = filepath.WalkDir(payload, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				relative, err := filepath.Rel(payload, path)
				if err != nil {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if strings.Contains(string(data), "MUST_NOT_SHIP") {
					t.Errorf("private content shipped in %s", relative)
				}
				files[filepath.ToSlash(relative)] = string(data)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range packageDocumentation() {
				if files[path] != "fixture "+path {
					t.Errorf("required archive file missing or changed: %s", path)
				}
			}
			if len(files) != len(packageDocumentation())+3 || files[packageBinary(target)] != string(fixtureBinary(target)) ||
				files["LICENSES/copilot-runtime.license"] != "runtime terms" ||
				files["LICENSES/dependency.license"] != "fixture dependency license" {
				t.Fatalf("archive has missing, changed, or unexpected payloads: %v", files)
			}
			info, err := os.Stat(filepath.Join(payload, packageBinary(target)))
			if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0755 {
				t.Fatalf("native executable mode: %v, %v", info, err)
			}
			data, err := os.ReadFile(filepath.Join(root, "dist", archive))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			checksum, err := os.ReadFile(filepath.Join(root, "dist", archive+".sha256"))
			if err != nil || string(checksum) != hex.EncodeToString(sum[:])+"  "+archive+"\n" {
				t.Fatalf("incorrect checksum: %q, %v", checksum, err)
			}
			for _, path := range []string{"unrelated-private-note", ".sodapop.env", "LICENSES/.sodapop.env", "LICENSES/unrelated-private-note"} {
				content, err := os.ReadFile(filepath.Join(root, "dist", name, filepath.FromSlash(path)))
				if err != nil || string(content) != "STALE_STAGING_CONTENT_MUST_NOT_SHIP" {
					t.Fatalf("packaging mutated existing staging content %s: %q, %v", path, content, err)
				}
			}
			for _, path := range nativeWebsiteIsolationFiles() {
				content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
				if err != nil || string(content) != "WEBSITE_CONTENT_MUST_NOT_SHIP "+path {
					t.Fatalf("packaging mutated excluded website fixture %s: %q, %v", path, content, err)
				}
			}
			assertNoPackageStaging(t, root)
		})
	}
}

func TestPackageRequiresProjectLicenseAndLinkedDocumentation(t *testing.T) {
	for _, missing := range append(packageDocumentation(), "internal/runtimebundle/zcopilot_1.0.83_linux_amd64.license") {
		t.Run(missing, func(t *testing.T) {
			root := packageFixture(t)
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(missing))); err != nil {
				t.Fatal(err)
			}
			if output, err := runPackage(t, root); err == nil {
				t.Fatalf("packaging succeeded without %s: %s", missing, output)
			}
			if _, err := os.Stat(filepath.Join(root, "dist", distribution.ArchiveName(packageVersion, "linux/amd64"))); !os.IsNotExist(err) {
				t.Fatalf("failed packaging produced an archive: %v", err)
			}
			assertNoPackageStaging(t, root)
		})
	}
}

func TestReleaseArchiveExecutionRequiresProductionVerification(t *testing.T) {
	targets := []string{"linux/amd64", "windows/amd64", "windows/arm64"}
	if runtime.GOOS == "windows" {
		targets = []string{"windows/amd64", "windows/arm64"}
	}
	for _, target := range targets {
		for _, corrupt := range []bool{false, true} {
			root := packageFixture(t)
			if output, err := runPackage(t, root, "SODAPOP_TARGET="+target); err != nil {
				t.Fatalf("package fixture failed: %s: %v", output, err)
			}
			packageManifest(t, root, []string{target})
			if corrupt {
				archive := filepath.Join(root, "dist", distribution.ArchiveName(packageVersion, target))
				data, err := os.ReadFile(archive)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(archive, append(data, []byte("corrupt")...), 0644); err != nil {
					t.Fatal(err)
				}
			}
			extracted := filepath.Join(root, "extracted")
			output, err := runScriptFixture(t, root, []string{releaseHelper(t), "extract",
				"--dir", "dist", "--manifest", "dist/sodapop-" + packageVersion + "-manifest.json",
				"--platform", target, "--output", extracted})
			if (err != nil) != corrupt {
				t.Fatalf("release CLI did not enforce verification: %s: %v", output, err)
			}
			if corrupt {
				if _, err := os.Stat(extracted); !os.IsNotExist(err) {
					t.Fatalf("failed verification created extraction output: %v", err)
				}
				continue
			}
			binary := filepath.Join(extracted, distribution.PackageName(packageVersion, target), packageBinary(target))
			if strings.HasPrefix(target, "windows/") {
				data, err := os.ReadFile(binary)
				if err != nil || !bytes.Equal(data, fixtureBinary(target)) {
					t.Fatalf("verified Windows archive fixture: %x, %v", data, err)
				}
				continue
			}
			for _, argument := range []string{"--help", "--version", "--check-runtime"} {
				output, err := runScriptFixture(t, root, []string{"bash", binary, argument})
				if err != nil || output != "fixture Sodapop launched\n" {
					t.Fatalf("verified archive fixture execution: %s: %v", output, err)
				}
			}
		}
	}
}

func TestReleaseManifestDescribesVerifiedNativeArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable permission fixtures are covered on Unix runners")
	}
	root := packageFixture(t)
	for _, target := range distribution.DefaultPlatforms() {
		if output, err := runPackage(t, root, "SODAPOP_TARGET="+target); err != nil {
			t.Fatalf("package %s failed: %s: %v", target, output, err)
		}
	}
	manifest := packageManifest(t, root, nil)
	if manifest.SchemaVersion != 1 || manifest.Version != packageVersion || manifest.Commit != strings.Repeat("a", 40) ||
		manifest.CopilotRuntimeVersion != "1.0.83" || manifest.CopilotSDKVersion != "1.0.13" || len(manifest.Artifacts) != 6 {
		t.Fatalf("unexpected release manifest metadata: %+v", manifest)
	}
	binaryHashes := make(map[string]string)
	for index, artifact := range manifest.Artifacts {
		if artifact.Platform != distribution.DefaultPlatforms()[index] {
			t.Fatalf("noncanonical artifact ordering: %+v", manifest.Artifacts)
		}
		archive, err := os.ReadFile(filepath.Join(root, "dist", artifact.Archive))
		if err != nil {
			t.Fatal(err)
		}
		archiveSum := sha256.Sum256(archive)
		binarySum := sha256.Sum256(fixtureBinary(artifact.Platform))
		binaryHashes[artifact.Platform] = hex.EncodeToString(binarySum[:])
		if artifact.Archive != distribution.ArchiveName(packageVersion, artifact.Platform) ||
			artifact.ArchiveSHA256 != hex.EncodeToString(archiveSum[:]) || artifact.BinarySHA256 != binaryHashes[artifact.Platform] {
			t.Fatalf("artifact hashes or name are incorrect: %+v", artifact)
		}
	}
	if binaryHashes["windows/amd64"] == binaryHashes["windows/arm64"] {
		t.Fatal("Windows architecture fixtures must have distinct binary hashes")
	}
}

func TestReleaseManifestRejectsIncompleteOrCorruptArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable permission fixtures are covered on Unix runners")
	}
	for _, mutation := range []string{"missing", "checksum", "multiline"} {
		root := packageFixture(t)
		for _, target := range distribution.DefaultPlatforms() {
			if output, err := runPackage(t, root, "SODAPOP_TARGET="+target); err != nil {
				t.Fatalf("package %s failed: %s: %v", target, output, err)
			}
		}
		archive := "dist/" + distribution.ArchiveName(packageVersion, "linux/amd64")
		switch mutation {
		case "missing":
			if err := os.Remove(filepath.Join(root, archive)); err != nil {
				t.Fatal(err)
			}
		case "checksum":
			writeFixtureFile(t, root, archive, "corrupt bytes", 0644)
		case "multiline":
			checksum, err := os.ReadFile(filepath.Join(root, archive+".sha256"))
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, root, archive+".sha256", string(checksum)+"\n", 0644)
		}
		output, err := runScriptFixture(t, root, []string{"bash", "scripts/release-manifest.sh"},
			"SODAPOP_VERSION="+packageVersion, "SODAPOP_COMMIT="+strings.Repeat("b", 40))
		if err == nil {
			t.Fatalf("manifest accepted %s artifact: %s", mutation, output)
		}
		if _, err := os.Stat(filepath.Join(root, "dist", "sodapop-"+packageVersion+"-manifest.json")); !os.IsNotExist(err) {
			t.Fatalf("failed generation left manifest: %v", err)
		}
	}
}

func TestPackageRefusesExistingOutputsBeforeBuild(t *testing.T) {
	for _, target := range []string{"linux/amd64", "windows/amd64", "windows/arm64"} {
		for _, suffix := range []string{"", ".sha256"} {
			root := packageFixture(t)
			name := "dist/" + distribution.ArchiveName(packageVersion, target) + suffix
			writeFixtureFile(t, root, name, "keep unrelated output", 0600)
			output, err := runPackage(t, root, "SODAPOP_TARGET="+target)
			if err == nil || !strings.Contains(output, "Refusing to overwrite") {
				t.Fatalf("existing output accepted: %s: %v", output, err)
			}
			assertFixtureLines(t, root, name, []string{"keep unrelated output"})
			if _, err := os.Stat(filepath.Join(root, "go-build.log")); !os.IsNotExist(err) {
				t.Fatalf("clobber rejection invoked build: %v", err)
			}
		}
	}
}

func TestReleaseManifestShellSubsetAndInvalidMetadata(t *testing.T) {
	for _, set := range []string{"", "linux/amd64,linux/amd64", "linux/amd64,windows/amd64", "linux/386"} {
		root := packageFixture(t)
		if output, err := runPackage(t, root); err != nil {
			t.Fatalf("package fixture: %s: %v", output, err)
		}
		if output, err := runScriptFixture(t, root, []string{"bash", "scripts/release-manifest.sh"},
			"SODAPOP_VERSION="+packageVersion, "SODAPOP_COMMIT="+strings.Repeat("b", 40), "SODAPOP_PLATFORMS="+set); err == nil {
			t.Fatalf("invalid/incomplete set %q accepted: %s", set, output)
		}
	}
	root := packageFixture(t)
	if output, err := runPackage(t, root); err != nil {
		t.Fatalf("package fixture: %s: %v", output, err)
	}
	manifest := packageManifest(t, root, []string{"linux/amd64"})
	manifest.Artifacts[0].BinarySHA256 = strings.Repeat("0", 64)
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "dist/modified.json", string(data), 0644)
	output, err := runScriptFixture(t, root, []string{releaseHelper(t), "verify", "--dir", "dist",
		"--manifest", "dist/modified.json", "--platform", "linux/amd64"})
	if err == nil || !strings.Contains(output, "binary SHA-256") {
		t.Fatalf("CLI accepted modified manifest: %s: %v", output, err)
	}
}

func assertNoPackageStaging(t *testing.T, root string) {
	t.Helper()
	for _, pattern := range []string{".sodapop-package.*", ".sodapop-archive-*"} {
		staging, err := filepath.Glob(filepath.Join(root, "dist", pattern))
		if err != nil || len(staging) != 0 {
			t.Fatalf("temporary package staging was not removed: %v, %v", staging, err)
		}
	}
}

func TestTaggedReleaseWorkflowCreatesDraftAssets(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, required := range []string{
		"tags:\n      - 'v*'",
		`version="${GITHUB_REF_NAME#v}"`,
		`printf 'SODAPOP_VERSION=%s\n' "$version" >> "$GITHUB_ENV"`,
		"SODAPOP_GITHUB_CLIENT_ID: ${{ vars.SODAPOP_GITHUB_CLIENT_ID }}",
		"target: darwin/arm64", "target: darwin/amd64", "target: linux/arm64", "target: linux/amd64",
		"target: windows/amd64", "target: windows/arm64",
		"needs: [package, installations, channels]", "contents: write",
		"npm run build --prefix npm", "name: npm-packages",
		"bash scripts/generate-homebrew-formula.sh", "name: homebrew-formula",
		`"$helper" manifest --dir dist`, "--platforms",
		`release_flags=(--draft)`,
		`if [[ "$SODAPOP_VERSION" == *-* ]]; then`,
		`release_flags+=(--prerelease)`,
		`gh release create "$GITHUB_REF_NAME"`, "--verify-tag", `"${release_flags[@]}"`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("release workflow is missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(workflow), "workflow_dispatch") {
		t.Error("tagged release workflow unexpectedly permits manual publishing")
	}
}
