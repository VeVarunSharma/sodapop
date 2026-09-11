package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var verifierBuild struct {
	sync.Once
	path string
	err  error
}

func TestMain(m *testing.M) {
	if os.Getenv("SODAPOP_WINDOWS_TEST_VERIFIER") == "1" {
		os.Exit(runTestVerifier(os.Args[1:]))
	}
	code := m.Run()
	if verifierBuild.path != "" {
		var err error
		for range 20 {
			if err = os.RemoveAll(filepath.Dir(verifierBuild.path)); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			panic(err)
		}
	}
	os.Exit(code)
}

func runTestVerifier(args []string) int {
	values := map[string]string{}
	for i := 1; i+1 < len(args); i += 2 {
		values[args[i]] = args[i+1]
	}
	if log := os.Getenv("SODAPOP_WINDOWS_TEST_VERIFIER_LOG"); log != "" {
		f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return 1
		}
		_, err = fmt.Fprintln(f, args[0], values["--platform"])
		if closeErr := f.Close(); err != nil || closeErr != nil {
			return 1
		}
	}
	if len(args) == 0 || (args[0] != "verify" && args[0] != "extract") {
		return 1
	}
	if args[0] == "verify" {
		return 0
	}
	data, err := os.ReadFile(values["--manifest"])
	if err != nil {
		return 1
	}
	var r release
	if json.Unmarshal(data, &r) != nil {
		return 1
	}
	var selected artifact
	for _, a := range r.Artifacts {
		if a.Platform == values["--platform"] {
			selected = a
		}
	}
	if selected.Platform == "" {
		return 1
	}
	reader, err := zip.OpenReader(filepath.Join(values["--dir"], selected.Archive))
	if err != nil {
		return 1
	}
	defer reader.Close()
	for _, entry := range reader.File {
		name := filepath.FromSlash(entry.Name)
		target := filepath.Join(values["--output"], name)
		if entry.FileInfo().IsDir() {
			if os.MkdirAll(target, 0755) != nil {
				return 1
			}
			continue
		}
		if os.MkdirAll(filepath.Dir(target), 0755) != nil {
			return 1
		}
		input, err := entry.Open()
		if err != nil {
			return 1
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			input.Close()
			return 1
		}
		_, copyErr := io.Copy(output, input)
		if errors.Join(copyErr, input.Close(), output.Close()) != nil {
			return 1
		}
	}
	return 0
}

func verifier(t *testing.T) string {
	t.Helper()
	verifierBuild.Do(func() {
		dir, err := os.MkdirTemp("", "sodapop-windows-verifier-")
		if err != nil {
			verifierBuild.err = err
			return
		}
		verifierBuild.path = filepath.Join(dir, "releasectl")
		if runtime.GOOS == "windows" {
			verifierBuild.path += ".exe"
		}
		command := exec.Command("go", "build", "-o", verifierBuild.path, "./scripts/releasectl")
		command.Dir = "../.."
		command.Env = append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
		var output []byte
		output, verifierBuild.err = command.CombinedOutput()
		if verifierBuild.err != nil {
			verifierBuild.err = &buildError{verifierBuild.err, string(output)}
		}
	})
	if verifierBuild.err != nil {
		t.Fatal(verifierBuild.err)
	}
	return verifierBuild.path
}

type buildError struct {
	err    error
	output string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.output }

func tempDir(t *testing.T) string {
	t.Helper()
	// macOS's /var temp alias is not an output-parent link we want to authorize.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

type fixture struct {
	dir, manifest string
	release       release
	binary        []byte
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func makeFixture(t *testing.T, version string, mutate func(map[string][]byte)) fixture {
	return makeArchitectureFixture(t, version, "windows/amd64", mutate)
}

func makeArchitectureFixture(t *testing.T, version, platform string, mutate func(map[string][]byte)) fixture {
	t.Helper()
	dir := tempDir(t)
	root := archiveRoot(version, platform)
	binary := []byte("MZ-fixture-not-a-native-executable")
	files := map[string][]byte{
		root + "/sodapop.exe":                          binary,
		root + "/LICENSE":                              []byte("Sodapop source license"),
		root + "/README.md":                            []byte("readme"),
		root + "/THIRD_PARTY_NOTICES.md":               []byte("separate runtime terms"),
		root + "/LICENSES/copilot-runtime.license":     []byte("runtime terms, not MIT"),
		root + "/LICENSES/example.org__lib@v1/LICENSE": []byte("dependency license"),
	}
	if mutate != nil {
		mutate(files)
	}
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	for name, body := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0644)
		entry, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archiveName := root + ".zip"
	writeFixture(t, filepath.Join(dir, archiveName), archive.Bytes())
	writeFixture(t, filepath.Join(dir, archiveName+".sha256"), []byte(hash(archive.Bytes())+"  "+archiveName+"\n"))
	r := release{
		SchemaVersion: 1, Version: version, Commit: strings.Repeat("a", 40),
		SDKVersion: "1.0.13", RuntimeVersion: "1.0.83",
		Artifacts: []artifact{{platform, archiveName, hash(archive.Bytes()), hash(binary)}},
	}
	manifest := filepath.Join(dir, "sodapop-"+version+"-manifest.json")
	f := fixture{dir, manifest, r, binary}
	f.writeManifest(t)
	return f
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (f fixture) writeManifest(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(f.release)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, f.manifest, data)
}

func (f fixture) args(t *testing.T, command, output string) []string {
	t.Helper()
	return []string{command, "--dir", f.dir, "--manifest", f.manifest, "--output", output, "--releasectl", verifier(t)}
}

func addArchitecture(t *testing.T, f *fixture, platform string) {
	t.Helper()
	other := makeArchitectureFixture(t, f.release.Version, platform, nil)
	a := other.release.Artifacts[0]
	for _, name := range []string{a.Archive, a.Archive + ".sha256"} {
		writeFixture(t, filepath.Join(f.dir, name), readFixture(t, filepath.Join(other.dir, name)))
	}
	f.release.Artifacts = append(f.release.Artifacts, a)
	f.writeManifest(t)
}

func TestProductionManifestGenerator(t *testing.T) {
	f := makeFixture(t, "1.2.3", nil)
	out := filepath.Join(f.dir, "generated")
	if err := run(f.args(t, "manifests", out)); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(out, "winget/manifests/v/VeVarunSharma/Sodapop/1.2.3")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("expected multi-YAML manifest, entries=%v error=%v", entries, err)
	}
	installer := string(readFixture(t, filepath.Join(dir, "VeVarunSharma.Sodapop.installer.yaml")))
	for _, want := range []string{
		"InstallerType: zip\nNestedInstallerType: portable",
		"RelativeFilePath: 'sodapop-1.2.3-windows-amd64\\sodapop.exe'",
		"PortableCommandAlias: sodapop", "Architecture: x64", "Scope: user",
		"ElevationRequirement: elevationProhibited",
		"InstallerSha256: '" + strings.ToUpper(f.release.Artifacts[0].ArchiveSHA256) + "'",
		repository + "/releases/download/v1.2.3/sodapop-1.2.3-windows-amd64.zip",
		"ManifestVersion: 1.9.0",
	} {
		if !strings.Contains(installer, want) {
			t.Fatalf("installer missing %q:\n%s", want, installer)
		}
	}
	if strings.Contains(installer, "SignatureSha256") || strings.Contains(installer, "Architecture: arm64") {
		t.Fatal("ZIP signature or unsupported native architecture advertised")
	}
	var scoop struct {
		Version      string                       `json:"version"`
		Architecture map[string]map[string]string `json:"architecture"`
		Bin          string                       `json:"bin"`
		License      map[string]string            `json:"license"`
		Notes        []string                     `json:"notes"`
	}
	if err := json.Unmarshal(readFixture(t, filepath.Join(out, "scoop/bucket/sodapop.json")), &scoop); err != nil {
		t.Fatal(err)
	}
	arch := scoop.Architecture["64bit"]
	if len(scoop.Architecture) != 1 || arch["extract_dir"] != archiveRoot("1.2.3", "windows/amd64") ||
		arch["hash"] != f.release.Artifacts[0].ArchiveSHA256 || scoop.Bin != "sodapop.exe" ||
		!strings.Contains(scoop.License["identifier"], "Proprietary") {
		t.Fatalf("incorrect Scoop manifest: %+v", scoop)
	}
	if err := run(f.args(t, "manifests", out)); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("stale output accepted: %v", err)
	}
}

func TestDualArchitectureManifestGenerator(t *testing.T) {
	f := makeFixture(t, "1.2.3", nil)
	addArchitecture(t, &f, "windows/arm64")
	t.Setenv("SODAPOP_WINDOWS_TEST_VERIFIER", "1")
	log := filepath.Join(f.dir, "verifier.log")
	t.Setenv("SODAPOP_WINDOWS_TEST_VERIFIER_LOG", log)
	out := filepath.Join(f.dir, "generated")
	args := []string{"manifests", "--dir", f.dir, "--manifest", f.manifest, "--output", out, "--releasectl", os.Args[0]}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	installer := string(readFixture(t, filepath.Join(out,
		"winget/manifests/v/VeVarunSharma/Sodapop/1.2.3/VeVarunSharma.Sodapop.installer.yaml")))
	for _, want := range []string{
		"Architecture: x64",
		"Architecture: arm64",
		"sodapop-1.2.3-windows-amd64.zip",
		"sodapop-1.2.3-windows-arm64.zip",
		"RelativeFilePath: 'sodapop-1.2.3-windows-amd64\\sodapop.exe'",
		"RelativeFilePath: 'sodapop-1.2.3-windows-arm64\\sodapop.exe'",
	} {
		if !strings.Contains(installer, want) {
			t.Fatalf("dual WinGet manifest missing %q:\n%s", want, installer)
		}
	}
	if strings.Contains(installer, "  - arm64\n") || strings.Contains(installer, "  - x64\n") {
		t.Fatalf("dual manifest marked a supplied architecture unsupported:\n%s", installer)
	}
	var scoop struct {
		Architecture map[string]map[string]string `json:"architecture"`
	}
	if err := json.Unmarshal(readFixture(t, filepath.Join(out, "scoop/bucket/sodapop.json")), &scoop); err != nil {
		t.Fatal(err)
	}
	if len(scoop.Architecture) != 2 ||
		scoop.Architecture["64bit"]["extract_dir"] != archiveRoot("1.2.3", "windows/amd64") ||
		scoop.Architecture["arm64"]["extract_dir"] != archiveRoot("1.2.3", "windows/arm64") {
		t.Fatalf("incorrect dual Scoop manifest: %+v", scoop)
	}
	verifierLog := string(readFixture(t, log))
	for _, platform := range []string{"windows/amd64", "windows/arm64"} {
		if strings.Count(verifierLog, "verify "+platform+"\n") != 1 {
			t.Fatalf("verifier did not receive exactly one %s selection:\n%s", platform, verifierLog)
		}
	}
}

func TestARM64PortableAndMSIContracts(t *testing.T) {
	f := makeArchitectureFixture(t, "1.2.3", "windows/arm64", nil)
	t.Setenv("SODAPOP_WINDOWS_TEST_VERIFIER", "1")
	portable := filepath.Join(f.dir, "portable")
	args := []string{"portable", "--dir", f.dir, "--manifest", f.manifest, "--output", portable,
		"--platform", "windows/arm64", "--releasectl", os.Args[0]}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, filepath.Join(portable, archiveRoot("1.2.3", "windows/arm64"), "sodapop.exe")); !bytes.Equal(got, f.binary) {
		t.Fatal("ARM64 portable consumer changed source binary")
	}
	stage := filepath.Join(f.dir, "msi")
	args[0], args[6] = "prepare-msi", stage
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	var input msiInput
	if err := json.Unmarshal(readFixture(t, filepath.Join(stage, "msi-input.json")), &input); err != nil {
		t.Fatal(err)
	}
	if input.Platform != "windows/arm64" || input.Architecture != "arm64" || input.WixArch != "arm64" ||
		input.ProductCode != stableGUID("product:windows/arm64:1.2.3") ||
		input.ProductCode == stableGUID("product:windows/amd64:1.2.3") ||
		input.UpgradeCode != upgradeCode {
		t.Fatalf("invalid ARM64 MSI contract: %+v", input)
	}
}

func TestSelectedArchitectureMustBePresentAndMatch(t *testing.T) {
	f := makeFixture(t, "1.2.3", nil)
	t.Setenv("SODAPOP_WINDOWS_TEST_VERIFIER", "1")
	out := filepath.Join(f.dir, "portable")
	args := []string{"portable", "--dir", f.dir, "--manifest", f.manifest, "--output", out,
		"--platform", "windows/arm64", "--releasectl", os.Args[0]}
	if err := run(args); err == nil || !strings.Contains(err.Error(), "missing windows/arm64") {
		t.Fatalf("absent selected architecture accepted: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("absent architecture emitted output")
	}
	f.release.Artifacts[0].Platform = "windows/arm64"
	f.writeManifest(t)
	if _, _, err := readRelease(f.manifest); err == nil || !strings.Contains(err.Error(), "archive name") {
		t.Fatalf("mismatched platform/archive accepted: %v", err)
	}
}

func TestProductionPortableAndMSIStaging(t *testing.T) {
	f := makeFixture(t, "1.2.3", nil)
	portable := filepath.Join(f.dir, "portable")
	if err := run(f.args(t, "portable", portable)); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, filepath.Join(portable, archiveRoot("1.2.3", "windows/amd64"), "sodapop.exe")); !bytes.Equal(got, f.binary) {
		t.Fatal("portable consumer changed source binary")
	}
	stage := filepath.Join(f.dir, "msi")
	if err := run(f.args(t, "prepare-msi", stage)); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, filepath.Join(stage, "payload/sodapop.exe")); !bytes.Equal(got, f.binary) {
		t.Fatal("MSI staging changed source binary")
	}
	var input msiInput
	if err := json.Unmarshal(readFixture(t, filepath.Join(stage, "msi-input.json")), &input); err != nil {
		t.Fatal(err)
	}
	if input.SigningState != "unsigned-candidate" || input.UpgradeCode != upgradeCode ||
		input.Platform != "windows/amd64" || input.Architecture != "x64" || input.WixArch != "x64" ||
		input.ProductCode != stableGUID("product:windows/amd64:1.2.3") || input.BinaryHash != hash(f.binary) || len(input.Files) != 6 {
		t.Fatalf("invalid MSI input: %+v", input)
	}
	wxs := readFixture(t, filepath.Join(stage, "Payload.wxs"))
	decoder := xml.NewDecoder(bytes.NewReader(wxs))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid WXS XML: %v", err)
		}
	}
	for _, forbidden := range []string{"CustomAction", "HKLM", "*.dll", "RemoveFile"} {
		if bytes.Contains(wxs, []byte(forbidden)) {
			t.Fatalf("unexpected generated installer operation: %s", forbidden)
		}
	}
	for _, entry := range input.Files {
		if got := hash(readFixture(t, filepath.Join(stage, "payload", filepath.FromSlash(entry.Path)))); got != entry.SHA256 {
			t.Fatalf("staged hash mismatch: %s", entry.Path)
		}
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".verified-extract-") {
			t.Fatal("private extraction staging was not cleaned")
		}
	}
}

func TestProductionMSIStagingFlatNotices(t *testing.T) {
	for _, name := range []string{"dependency.license", "dependency.notice"} {
		t.Run(name, func(t *testing.T) {
			body := []byte("independent dependency license terms")
			f := makeFixture(t, "0.0.2", func(files map[string][]byte) {
				root := archiveRoot("0.0.2", "windows/amd64")
				delete(files, root+"/LICENSES/example.org__lib@v1/LICENSE")
				files[root+"/LICENSES/"+name] = body
			})
			stage := filepath.Join(f.dir, "msi")
			if err := run(f.args(t, "prepare-msi", stage)); err != nil {
				t.Fatal(err)
			}
			if got := readFixture(t, filepath.Join(stage, "payload/LICENSES", name)); !bytes.Equal(got, body) {
				t.Fatal("flat dependency notice bytes changed during MSI staging")
			}
			if got := readFixture(t, filepath.Join(stage, "payload/sodapop.exe")); !bytes.Equal(got, f.binary) {
				t.Fatal("source executable bytes changed during MSI staging")
			}
		})
	}
}

func TestProductionSemverAndCustomIdentifier(t *testing.T) {
	f := makeFixture(t, "1.2.3-rc.1+build.01", nil)
	out := filepath.Join(f.dir, "generated")
	args := append(f.args(t, "manifests", out), "--package-id", "Owner.CLI", "--tag", "v"+f.release.Version)
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	installer := string(readFixture(t, filepath.Join(out, "winget/manifests/o/Owner/CLI", f.release.Version, "Owner.CLI.installer.yaml")))
	if !strings.Contains(installer, "PackageVersion: '1.2.3-rc.1+build.01'") ||
		!strings.Contains(installer, "/releases/download/v1.2.3-rc.1+build.01/sodapop-1.2.3-rc.1+build.01-windows-amd64.zip") {
		t.Fatal("ZIP SemVer was truncated or rewritten")
	}
	if err := run(f.args(t, "prepare-msi", filepath.Join(f.dir, "msi"))); err == nil || !strings.Contains(err.Error(), "MSI requires numeric") {
		t.Fatalf("MSI accepted a prerelease/build-metadata version: %v", err)
	}
}

func TestProductionManifestRejectsDuplicateKeys(t *testing.T) {
	f := makeFixture(t, "1.2.3", nil)
	manifest := readFixture(t, f.manifest)
	manifest = bytes.Replace(manifest, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)
	writeFixture(t, f.manifest, manifest)
	out := filepath.Join(f.dir, "generated")
	if err := run(f.args(t, "manifests", out)); err == nil {
		t.Fatal("duplicate manifest keys passed the shared verifier")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("duplicate keys emitted channel output")
	}
}

func TestProductionGeneratorRejectsUnverifiedArchives(t *testing.T) {
	for _, kind := range []string{"archive-hash", "binary-hash", "wrong-root", "traversal", "extra-dll", "missing-notices"} {
		t.Run(kind, func(t *testing.T) {
			f := makeFixture(t, "1.2.3", func(files map[string][]byte) {
				switch kind {
				case "wrong-root":
					for name, body := range files {
						delete(files, name)
						files[strings.Replace(name, "1.2.3", "1.2.4", 1)] = body
					}
				case "traversal":
					files[archiveRoot("1.2.3", "windows/amd64")+"/../escape"] = []byte("bad")
				case "extra-dll":
					files[archiveRoot("1.2.3", "windows/amd64")+"/stale.dll"] = []byte("bad")
				case "missing-notices":
					delete(files, archiveRoot("1.2.3", "windows/amd64")+"/THIRD_PARTY_NOTICES.md")
				}
			})
			switch kind {
			case "archive-hash":
				writeFixture(t, filepath.Join(f.dir, f.release.Artifacts[0].Archive), []byte("corrupt download"))
			case "binary-hash":
				f.release.Artifacts[0].BinarySHA256 = strings.Repeat("1", 64)
				f.writeManifest(t)
			}
			out := filepath.Join(f.dir, "result")
			if err := run(f.args(t, "manifests", out)); err == nil {
				t.Fatal("unverified archive generated manifests")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("failure emitted a success-shaped output")
			}
		})
	}
}

func TestInvalidMetadataAndArguments(t *testing.T) {
	for name, change := range map[string]func(*release){
		"schema":            func(r *release) { r.SchemaVersion = 2 },
		"version-injection": func(r *release) { r.Version = "1.2.3'\nScope: machine" },
		"version-path":      func(r *release) { r.Version = "../1.2.3" },
		"commit":            func(r *release) { r.Commit = "main" },
		"sdk":               func(r *release) { r.SDKVersion = "latest" },
		"runtime":           func(r *release) { r.RuntimeVersion = "1.0.1\nbad" },
		"archive-path":      func(r *release) { r.Artifacts[0].Archive = "../bad.zip" },
		"archive-version":   func(r *release) { r.Artifacts[0].Archive = "sodapop-1.2.4-windows-amd64.zip" },
		"archive-hash":      func(r *release) { r.Artifacts[0].ArchiveSHA256 = "not-a-digest" },
		"binary-hash":       func(r *release) { r.Artifacts[0].BinarySHA256 = strings.Repeat("x", 64) },
		"duplicate":         func(r *release) { r.Artifacts = append(r.Artifacts, r.Artifacts[0]) },
		"unsupported-only":  func(r *release) { r.Artifacts[0].Platform = "windows/386" },
	} {
		t.Run(name, func(t *testing.T) {
			f := makeFixture(t, "1.2.3", nil)
			change(&f.release)
			f.writeManifest(t)
			_, _, err := readRelease(f.manifest)
			if err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	f := makeFixture(t, "1.2.3", nil)
	for _, data := range [][]byte{
		append(readFixture(t, f.manifest), []byte("\n{}")...),
		[]byte(`{"schema_version":1, "unexpected":true}`),
		bytes.Repeat([]byte(" "), (1<<20)+1),
	} {
		writeFixture(t, f.manifest, data)
		if _, _, err := readRelease(f.manifest); err == nil {
			t.Fatal("malformed/oversized manifest accepted")
		}
	}
	for _, args := range [][]string{nil, {"unknown"}, {"manifests"}, {"manifests", "--unknown"}, {"portable", "unexpected"}} {
		if err := run(args); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestVersionRulesAndIdentifiers(t *testing.T) {
	for _, v := range []string{"0.0.1", "255.255.65535", "1.2.3-rc.1", "1.2.3-alpha-2", "1.2.3+build", "1.2.3-rc.1+build.01"} {
		if !validVersion(v) {
			t.Fatalf("valid ZIP version rejected: %s", v)
		}
	}
	for _, v := range []string{"", "v1.2.3", "01.2.3", "1.02.3", "1.2", "1.2.3.4", "1.2.3+", "1.2.3-01", "1.2.3-", "1.2.3-rc..1", "1.2.3/../x"} {
		if validVersion(v) || msiVersion(v) == nil {
			t.Fatalf("invalid version accepted: %s", v)
		}
	}
	for _, v := range []string{"0.0.1", "255.255.65535"} {
		if err := msiVersion(v); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []string{"256.0.0", "1.256.0", "1.2.65536", "1.2.3-rc.1", "1.2.3+build", "9999999999999999.2.3"} {
		if msiVersion(v) == nil {
			t.Fatalf("unsupported MSI version accepted: %s", v)
		}
	}
	if stableGUID("product:1.2.3") == stableGUID("product:1.2.4") || stableGUID("product:1.2.3") != stableGUID("product:1.2.3") {
		t.Fatal("product identifiers are not version-specific and stable")
	}
	if len(architectures) != 2 || architectures["windows/amd64"].winget != "x64" ||
		architectures["windows/arm64"].winget != "arm64" {
		t.Fatal("Windows architecture mapping is incomplete")
	}
	f := makeFixture(t, "1.2.3", nil)
	for _, extra := range [][]string{
		{"--package-id", "../Bad.ID"}, {"--package-id", "Publisher.App'\nBad"}, {"--tag", "main"}, {"--tag", "1.2.3"},
	} {
		out := filepath.Join(f.dir, "invalid")
		args := []string{"manifests", "--dir", f.dir, "--manifest", f.manifest, "--output", out, "--releasectl", "missing"}
		if err := run(append(args, extra...)); err == nil || strings.Contains(err.Error(), "shared release") {
			t.Fatalf("invalid option not rejected before verifier: %v", err)
		}
	}
}

func TestEscapingAndBoundedPaths(t *testing.T) {
	if got := yamlString("owner's: app #1"); got != "'owner''s: app #1'" {
		t.Fatalf("unsafe YAML escaping: %s", got)
	}
	if got := xmlAttribute(`a"&<b>`); strings.Contains(got, `"`) || !strings.Contains(got, "&amp;") {
		t.Fatalf("unsafe XML escaping: %s", got)
	}
	for _, p := range []string{"../escape", "LICENSES//foo", "C:/bad", "LICENSES/CON.txt", "LICENSES/LPT1", "LICENSES/a.", `LICENSES\a`, "LICENSES/\u202eevil", strings.Repeat("a", 181)} {
		if safePayloadPath(p) == nil {
			t.Fatalf("unsafe path accepted: %q", p)
		}
	}
	dir := tempDir(t)
	existing := filepath.Join(dir, "existing")
	writeFixture(t, existing, []byte("sentinel"))
	if err := newDestination(existing); err == nil {
		t.Fatal("existing file accepted")
	}
	if err := newDestination(filepath.Join(dir, "missing-parent", "out")); err == nil {
		t.Fatal("missing parent accepted")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(dir, "link")
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		if err := newDestination(filepath.Join(link, "out")); err == nil {
			t.Fatal("symlink output parent accepted")
		}
		manifestLink := filepath.Join(dir, "manifest-link")
		if err := os.Symlink(existing, manifestLink); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readRelease(manifestLink); err == nil {
			t.Fatal("symlink manifest accepted")
		}
	}
	if err := writeNew(existing, []byte("replacement")); err == nil {
		t.Fatal("stale output file overwritten")
	}
	if string(readFixture(t, existing)) != "sentinel" {
		t.Fatal("existing output changed")
	}
}

func TestPayloadAllowlist(t *testing.T) {
	for _, name := range []string{"LICENSE", "LICENSE.txt", "NOTICE.md", "LICENSE-BSD", "LICENSE.APACHE", "COPYING", "dependency.license", "dependency.NOTICE"} {
		if !isNotice(name) {
			t.Fatalf("notice rejected: %s", name)
		}
	}
	for _, name := range []string{"stale.dll", "LICENSE.exe", "NOTICE.ps1", ".env", "licensehelper", ".license", ".notice", "dependency.license.exe"} {
		if isNotice(name) {
			t.Fatalf("non-notice accepted: %s", name)
		}
	}
	root := tempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "LICENSES"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"sodapop.exe", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSES/copilot-runtime.license"} {
		writeFixture(t, filepath.Join(root, p), []byte("fixture"))
	}
	if _, err := payloadFiles(root); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "stale.dll")
	writeFixture(t, stale, []byte("not allowlisted"))
	if _, err := payloadFiles(root); err == nil {
		t.Fatal("stale DLL accepted")
	}
	if err := os.Remove(stale); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	if _, err := payloadFiles(root); err == nil {
		t.Fatal("missing source license accepted")
	}
}

func TestStaticPerUserRecipe(t *testing.T) {
	wxs := string(readFixture(t, "../../packaging/windows/Sodapop.wxs"))
	for _, want := range []string{
		`Scope="perUser"`, `Id="LocalAppDataFolder"`, `Name="Sodapop"`,
		`System="no" Permanent="no"`, `Part="last"`, `Root="HKCU"`, upgradeCode,
		`ProductCode="$(ProductCode)"`, `Schedule="afterInstallInitialize"`,
	} {
		if !strings.Contains(wxs, want) {
			t.Fatalf("missing per-user contract: %s", want)
		}
	}
	for _, forbidden := range []string{"CustomAction", "HKLM", "ProgramFilesFolder", "RemoveFile", "RemoveFolderEx", "perMachine"} {
		if strings.Contains(wxs, forbidden) {
			t.Fatalf("unsafe installer authoring: %s", forbidden)
		}
	}
	var tools struct {
		IsRoot bool `json:"isRoot"`
		Tools  map[string]struct {
			Version string `json:"version"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(readFixture(t, "../../packaging/windows/.config/dotnet-tools.json"), &tools); err != nil {
		t.Fatal(err)
	}
	if !tools.IsRoot || len(tools.Tools) != 1 || tools.Tools["wix"].Version != "7.0.0" {
		t.Fatal("WiX must use an exact, isolated local tool pin")
	}
}

func TestNativeMSILifecycleSafetyGates(t *testing.T) {
	common := string(readFixture(t, "Common.ps1"))
	for _, required := range []string{
		`$env:SODAPOP_WINDOWS_DISPOSABLE -cne '1'`,
		`$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)`,
		`throw 'Run installer lifecycle tests as a non-elevated user`,
	} {
		if !strings.Contains(common, required) {
			t.Fatalf("missing disposable-runner safety gate: %s", required)
		}
	}

	script := string(readFixture(t, "Test-Native.ps1"))
	consent := strings.Index(script, "if ($MsiLifecycle) { Assert-DisposableRunner $platform }")
	work := strings.Index(script, "$work = Get-NewDirectoryPath")
	related := strings.Index(script, `$installer.RelatedProducts("{$($new.Record.upgrade_code)}")`)
	rejection := strings.Index(script, "if ($relatedProducts.Count -ne 0)")
	sentinels := strings.Index(script, "$sentinelName =")
	install := strings.Index(script, "Invoke-Msi '/i' $old")
	if consent < 0 || work < consent || related < work || rejection < related || sentinels < rejection || install < sentinels {
		t.Fatal("disposable consent and all-related-product refusal must precede state mutation and MSI installation")
	}
	for _, required := range []string{
		"Refusing an existing registered or advertised Sodapop MSI",
		"Refusing a pre-existing Sodapop installation",
		"MSI upgrade needs two strictly increasing numeric release versions",
		"[Runtime.InteropServices.Marshal]::ReleaseComObject($installer)",
		"$record.platform -cne $platform",
		"$record.wix_architecture -cne $Architecture",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing native safety contract: %s", required)
		}
	}
}

func TestStaticArchitectureAwarePowerShellContracts(t *testing.T) {
	common := string(readFixture(t, "Common.ps1"))
	for _, required := range []string{
		"function Get-NativeWindowsPlatform",
		"function Assert-WindowsExecutableArchitecture",
		"[Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture",
		"if ($processArchitecture -ne $osArchitecture)",
		"Expected native $ExpectedPlatform",
		"'windows/amd64'",
		"'windows/arm64'",
	} {
		if !strings.Contains(common, required) {
			t.Fatalf("missing native architecture contract: %s", required)
		}
	}
	for _, file := range []string{
		"Build-Msi.ps1", "Complete-Msi.ps1", "Install-Portable.ps1",
		"Restore-Wix.ps1", "Test-Channels.ps1", "Test-Native.ps1",
	} {
		if strings.Contains(string(readFixture(t, file)), "Assert-WindowsX64") {
			t.Fatalf("%s still uses the x64-only native gate", file)
		}
	}
	build := string(readFixture(t, "Build-Msi.ps1"))
	for _, required := range []string{
		"'--platform', $platform",
		"'-arch', $inputMetadata.wix_architecture",
		"$platform.Replace('/', '-')",
		`$productSource.Replace('<MajorUpgrade ',`,
		`'<MajorUpgrade AllowSameVersionUpgrades="yes" ')`,
	} {
		if !strings.Contains(build, required) {
			t.Fatalf("Build-Msi.ps1 missing architecture parameterization: %s", required)
		}
	}
	complete := string(readFixture(t, "Complete-Msi.ps1"))
	for _, required := range []string{
		"platform = $inputData.platform",
		"architecture = $inputData.architecture",
		"wix_architecture = $inputData.wix_architecture",
		"MSI filename must be $expectedMsi",
	} {
		if !strings.Contains(complete, required) {
			t.Fatalf("Complete-Msi.ps1 missing architecture evidence: %s", required)
		}
	}
}

func TestPowerShellExecutableArchitectureValidation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell PE validation is Windows-specific")
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is not installed")
	}
	writePE := func(name string, machine uint16) string {
		t.Helper()
		data := make([]byte, 0x86)
		binary.LittleEndian.PutUint16(data, 0x5A4D)
		binary.LittleEndian.PutUint32(data[0x3C:], 0x80)
		binary.LittleEndian.PutUint32(data[0x80:], 0x00004550)
		binary.LittleEndian.PutUint16(data[0x84:], machine)
		filename := filepath.Join(t.TempDir(), name)
		writeFixture(t, filename, data)
		return filename
	}
	x64 := writePE("x64.exe", 0x8664)
	arm64 := writePE("arm64.exe", 0xAA64)
	common, err := filepath.Abs("Common.ps1")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	script := ". " + quote(common) +
		"; Assert-WindowsExecutableArchitecture " + quote(x64) + " 'windows/amd64'" +
		"; Assert-WindowsExecutableArchitecture " + quote(arm64) + " 'windows/arm64'"
	command := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("PowerShell PE architecture validation failed: %v\n%s", err, result)
	}
	mismatch := ". " + quote(common) +
		"; Assert-WindowsExecutableArchitecture " + quote(x64) + " 'windows/arm64'"
	command = exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", mismatch)
	if result, err := command.CombinedOutput(); err == nil {
		t.Fatalf("PowerShell PE architecture mismatch was accepted:\n%s", result)
	}
}

func TestGetNewDirectoryPathTraversesAncestors(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell filesystem behavior is Windows-specific")
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is not installed")
	}
	common, err := filepath.Abs("Common.ps1")
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(t.TempDir(), "one", "two")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(parent, "new-output")
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	script := ". " + quote(common) + "; $actual = Get-NewDirectoryPath " + quote(output) +
		"; if ($actual -cne [IO.Path]::GetFullPath(" + quote(output) + ")) { throw 'Unexpected output path.' }"
	command := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Get-NewDirectoryPath failed: %v\n%s", err, result)
	}
}
