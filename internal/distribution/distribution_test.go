package distribution

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const testVersion = "1.2.3-rc.1+test"

func put(t *testing.T, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func pins(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	put(t, filepath.Join(repo, "internal/runtimebundle/version.go"), `package runtimebundle
const ( Version = "1.0.83"; SDKVersion = "1.0.13" )
`, 0644)
	put(t, filepath.Join(repo, "go.mod"), "module fixture\nrequire github.com/github/copilot-sdk/go v1.0.13\n", 0644)
	return repo
}

func stageFixture(t *testing.T, platform string) string {
	t.Helper()
	stage := filepath.Join(t.TempDir(), PackageName(testVersion, platform))
	for _, name := range append(slices.Clone(documentation), BinaryName(platform), "LICENSES/copilot-runtime.license", "LICENSES/github.com__dependency@v1.0.0/LICENSE") {
		mode := os.FileMode(0644)
		if name == BinaryName(platform) {
			mode = 0755
		}
		put(t, filepath.Join(stage, filepath.FromSlash(name)), "fixture "+name+"\n", mode)
	}
	return stage
}

func releaseFixture(t *testing.T, platforms ...string) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	for _, platform := range platforms {
		if _, err := Archive(stageFixture(t, platform), dir, testVersion, platform); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Generate(dir, testVersion, strings.Repeat("A", 40), pins(t), platforms)
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReleaseRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable-mode archive fixtures are covered on Unix; Windows uses the native ZIP path")
	}
	dir, generated := releaseFixture(t, DefaultPlatforms()...)
	m, err := ReadManifest(filepath.Join(dir, "sodapop-"+testVersion+"-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Commit != strings.Repeat("a", 40) || m.CopilotSDKVersion != "1.0.13" ||
		m.CopilotRuntimeVersion != "1.0.83" || !slices.Equal(m.Artifacts, generated.Artifacts) {
		t.Fatalf("incorrect release metadata: %+v", m)
	}
	if err := Verify(dir, m, "", DefaultPlatforms()); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range m.Artifacts {
		for _, existing := range []bool{false, true} {
			output := filepath.Join(t.TempDir(), "extracted")
			if existing {
				if err := os.Mkdir(output, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := Extract(dir, m, artifact.Platform, output); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(output, PackageName(m.Version, artifact.Platform), BinaryName(artifact.Platform))
			if digest(read(t, binary)) != artifact.BinarySHA256 {
				t.Fatal("extracted binary differs from the verified release")
			}
			if err := Extract(dir, m, artifact.Platform, output); err == nil {
				t.Fatal("nonempty output was overwritten")
			}
		}
	}
	if _, err := Generate(dir, testVersion, m.Commit, pins(t), DefaultPlatforms()); err == nil {
		t.Fatal("existing manifest was overwritten")
	}
}

func TestSubsetPhasesRequireExplicitCompleteness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix archive fixture coverage is covered on Unix runners")
	}
	dir, m := releaseFixture(t, "linux/amd64")
	if err := Verify(dir, m, "", DefaultPlatforms()); err == nil {
		t.Fatal("incomplete full release was accepted")
	}
	if err := Verify(dir, m, "", []string{"linux/amd64"}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir, m, "linux/amd64", nil); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir, m, "windows/amd64", nil); err == nil {
		t.Fatal("missing selected platform accepted")
	}
	for _, targets := range [][]string{DefaultPlatforms(), nil, {"linux/amd64", "linux/amd64"}, {"freebsd/amd64"}} {
		if _, err := Generate(t.TempDir(), testVersion, m.Commit, pins(t), targets); err == nil {
			t.Fatalf("invalid/incomplete platforms accepted: %v", targets)
		}
	}
}

func TestManifestRejectsAmbiguousOrInvalidMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix archive fixture coverage is covered on Unix runners")
	}
	_, original := releaseFixture(t, "linux/amd64")
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"truncated":      `{"schema_version":`,
		"duplicate key":  strings.Replace(string(encoded), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"case alias":     strings.Replace(string(encoded), `"version":`, `"VERSION":`, 1),
		"unknown field":  strings.Replace(string(encoded), `"schema_version":1`, `"schema_version":1,"unknown":true`, 1),
		"null artifacts": strings.Replace(string(encoded), `"artifacts":[`, `"artifacts":null,"other":[`, 1),
		"trailing json":  string(encoded) + "{}",
		"oversize":       strings.Repeat(" ", 64<<10) + string(encoded),
		"deep nesting":   strings.Repeat("[", 12) + strings.Repeat("]", 12),
		"not object":     `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "manifest.json")
			put(t, file, data, 0644)
			if _, err := ReadManifest(file); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	cases := map[string]func(*Manifest){
		"schema":         func(m *Manifest) { m.SchemaVersion = 2 },
		"version":        func(m *Manifest) { m.Version = "../outside" },
		"commit":         func(m *Manifest) { m.Commit = "abcd" },
		"runtime pin":    func(m *Manifest) { m.CopilotRuntimeVersion = "" },
		"SDK pin":        func(m *Manifest) { m.CopilotSDKVersion = "" },
		"empty set":      func(m *Manifest) { m.Artifacts = nil },
		"duplicate":      func(m *Manifest) { m.Artifacts = append(m.Artifacts, m.Artifacts[0]) },
		"wrong platform": func(m *Manifest) { m.Artifacts[0].Platform = "linux/386" },
		"wrong version":  func(m *Manifest) { m.Artifacts[0].Archive = "sodapop-9.9.9-linux-amd64.tar.gz" },
		"wrong format":   func(m *Manifest) { m.Artifacts[0].Archive = PackageName(m.Version, "linux/amd64") + ".zip" },
		"archive hash":   func(m *Manifest) { m.Artifacts[0].ArchiveSHA256 = strings.Repeat("A", 64) },
		"binary hash":    func(m *Manifest) { m.Artifacts[0].BinarySHA256 = "abc" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := original
			m.Artifacts = slices.Clone(original.Artifacts)
			mutate(&m)
			if err := ValidateManifest(m, nil); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	if err := ValidateManifest(original, []string{"invalid"}); err == nil {
		t.Fatal("invalid expected set accepted")
	}
}

func TestManifestArtifactCardinalityError(t *testing.T) {
	m := Manifest{
		SchemaVersion:         1,
		Version:               testVersion,
		Commit:                strings.Repeat("a", 40),
		CopilotRuntimeVersion: "1.0.83",
		CopilotSDKVersion:     "1.0.13",
	}
	for _, count := range []int{0, 7} {
		m.Artifacts = make([]Artifact, count)
		err := ValidateManifest(m, nil)
		if err == nil || err.Error() != "manifest must contain between one and six artifacts" {
			t.Fatalf("artifact count %d error = %v", count, err)
		}
	}
}

type testEntry struct {
	name string
	mode os.FileMode
	body string
	kind byte
	link string
}

func validEntries(platform string) []testEntry {
	root := PackageName(testVersion, platform) + "/"
	var entries []testEntry
	for _, name := range []string{BinaryName(platform), "LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSES/copilot-runtime.license", "LICENSES/dependency.license"} {
		mode := os.FileMode(0644)
		if name == BinaryName(platform) {
			mode = 0755
		}
		entries = append(entries, testEntry{name: root + name, mode: mode, body: "fixture " + name, kind: tar.TypeReg})
	}
	return entries
}

func malformedArchive(t *testing.T, platform string, entries []testEntry, transform func([]byte) []byte) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	var buffer bytes.Buffer
	if BinaryName(platform) == "sodapop.exe" {
		writer := zip.NewWriter(&buffer)
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
			header.SetMode(entry.mode)
			stream, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(stream, entry.body); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&buffer)
		writer := tar.NewWriter(compressed)
		for _, entry := range entries {
			header := &tar.Header{Name: entry.name, Mode: int64(entry.mode.Perm()), Size: int64(len(entry.body)), Typeflag: entry.kind, Linkname: entry.link}
			if entry.kind == tar.TypeSymlink || entry.kind == tar.TypeLink || entry.kind == tar.TypeDir {
				header.Size = 0
			}
			if entry.mode&os.ModeSetuid != 0 {
				header.Mode |= 04000
			}
			if err := writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if header.Size != 0 {
				if _, err := io.WriteString(writer, entry.body); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	data := buffer.Bytes()
	if transform != nil {
		data = transform(data)
	}
	artifact := Artifact{Platform: platform, Archive: ArchiveName(testVersion, platform), ArchiveSHA256: digest(data), BinarySHA256: digest([]byte("fixture " + BinaryName(platform)))}
	put(t, filepath.Join(dir, artifact.Archive), string(data), 0644)
	put(t, filepath.Join(dir, artifact.Archive+".sha256"), artifact.ArchiveSHA256+"  "+artifact.Archive+"\n", 0644)
	m := Manifest{SchemaVersion: 1, Version: testVersion, Commit: strings.Repeat("a", 40), CopilotRuntimeVersion: "1.0.83", CopilotSDKVersion: "1.0.13", Artifacts: []Artifact{artifact}}
	return dir, m
}

func assertRejectedUnchanged(t *testing.T, dir string, m Manifest) {
	t.Helper()
	output := filepath.Join(t.TempDir(), "output")
	if err := os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir, m, m.Artifacts[0].Platform, nil); err == nil {
		t.Fatal("production verifier accepted invalid release")
	}
	if err := Extract(dir, m, m.Artifacts[0].Platform, output); err == nil {
		t.Fatal("production extractor accepted invalid release")
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed extraction changed output: %v, %v", entries, err)
	}
}

func TestVerifierRejectsUnsafeArchivePayloadsInBothFormats(t *testing.T) {
	for _, platform := range []string{"linux/amd64", "windows/amd64", "windows/arm64"} {
		cases := map[string]func([]testEntry) []testEntry{
			"duplicate binary": func(e []testEntry) []testEntry { return append(e, e[0]) },
			"case alias": func(e []testEntry) []testEntry {
				copy := e[1]
				copy.name = strings.TrimSuffix(copy.name, "LICENSE") + "license"
				return append(e, copy)
			},
			"private file": func(e []testEntry) []testEntry { e[1].name = PackageName(testVersion, platform) + "/.env"; return e },
			"traversal": func(e []testEntry) []testEntry {
				e[1].name = PackageName(testVersion, platform) + "/../outside"
				return e
			},
			"absolute":   func(e []testEntry) []testEntry { e[1].name = "/outside"; return e },
			"backslash":  func(e []testEntry) []testEntry { e[1].name = `package\outside`; return e },
			"drive path": func(e []testEntry) []testEntry { e[1].name = "C:/outside"; return e },
			"device": func(e []testEntry) []testEntry {
				e[1].name = PackageName(testVersion, platform) + "/LICENSES/NUL"
				return e
			},
			"other root": func(e []testEntry) []testEntry { e[1].name = "other/LICENSE"; return e },
			"trailing dot": func(e []testEntry) []testEntry {
				e[1].name += "."
				return e
			},
			"directory duplicate": func(e []testEntry) []testEntry {
				d := testEntry{name: PackageName(testVersion, platform) + "/LICENSES/", mode: os.ModeDir | 0755, kind: tar.TypeDir}
				return append(e, d, d)
			},
			"unexpected directory": func(e []testEntry) []testEntry {
				return append(e, testEntry{name: PackageName(testVersion, platform) + "/private/", mode: os.ModeDir | 0755, kind: tar.TypeDir})
			},
			"empty license": func(e []testEntry) []testEntry { e[1].body = ""; return e },
			"blank license": func(e []testEntry) []testEntry { e[1].body = " \t\r\n\x00"; return e },
			"implicit directory collision": func(e []testEntry) []testEntry {
				root := PackageName(testVersion, platform) + "/LICENSES/"
				return append(e,
					testEntry{name: root + "LICENSE@v1.0.0/NOTICE", mode: 0644, kind: tar.TypeReg, body: "notice"},
					testEntry{name: root + "LICENSE@v1.0.0", mode: 0644, kind: tar.TypeReg, body: "file"})
			},
			"parent file collision": func(e []testEntry) []testEntry {
				root := PackageName(testVersion, platform) + "/LICENSES/"
				return append(e,
					testEntry{name: root + "LICENSE@v1.0.0", mode: 0644, kind: tar.TypeReg, body: "file"},
					testEntry{name: root + "LICENSE@v1.0.0/NOTICE", mode: 0644, kind: tar.TypeReg, body: "notice"})
			},
			"missing license": func(e []testEntry) []testEntry {
				return append(e[:1], e[2:]...)
			},
			"missing notices":      func(e []testEntry) []testEntry { return append(e[:2], e[3:]...) },
			"missing runtime":      func(e []testEntry) []testEntry { return append(e[:3], e[4:]...) },
			"missing dependencies": func(e []testEntry) []testEntry { return e[:4] },
			"missing binary":       func(e []testEntry) []testEntry { return e[1:] },
			"symlink": func(e []testEntry) []testEntry {
				e[0].mode = os.ModeSymlink | 0755
				e[0].kind, e[0].link = tar.TypeSymlink, "outside"
				return e
			},
			"setuid": func(e []testEntry) []testEntry { e[0].mode |= os.ModeSetuid; return e },
		}
		if BinaryName(platform) != "sodapop.exe" {
			cases["hardlink"] = func(e []testEntry) []testEntry { e[0].kind, e[0].link = tar.TypeLink, "outside"; return e }
			cases["not executable"] = func(e []testEntry) []testEntry { e[0].mode = 0644; return e }
		}
		for name, mutate := range cases {
			t.Run(platform+"/"+name, func(t *testing.T) {
				dir, m := malformedArchive(t, platform, mutate(validEntries(platform)), nil)
				assertRejectedUnchanged(t, dir, m)
				if _, err := Generate(dir, testVersion, m.Commit, pins(t), []string{platform}); err == nil {
					t.Fatal("manifest generator accepted unsafe archive")
				}
			})
		}
	}
}

func TestVerifierRejectsChecksumsCorruptionAndBinaryMismatch(t *testing.T) {
	platforms := []string{"linux/amd64", "windows/amd64", "windows/arm64"}
	if runtime.GOOS == "windows" {
		platforms = []string{"windows/amd64", "windows/arm64"}
	}
	for _, platform := range platforms {
		for _, mutation := range []string{"archive bytes", "archive hash", "binary hash", "checksum basename", "multiline", "uppercase", "no newline", "empty", "short", "checksum symlink", "archive symlink", "manifest symlink"} {
			t.Run(platform+"/"+mutation, func(t *testing.T) {
				dir, m := releaseFixture(t, platform)
				a := &m.Artifacts[0]
				archive := filepath.Join(dir, a.Archive)
				checksum := archive + ".sha256"
				switch mutation {
				case "archive bytes":
					put(t, archive, string(read(t, archive))+"corrupt", 0644)
				case "archive hash":
					a.ArchiveSHA256 = strings.Repeat("0", 64)
				case "binary hash":
					a.BinarySHA256 = strings.Repeat("0", 64)
				case "checksum basename":
					put(t, checksum, a.ArchiveSHA256+"  other.tar.gz\n", 0644)
				case "multiline":
					put(t, checksum, string(read(t, checksum))+"\n", 0644)
				case "uppercase":
					put(t, checksum, strings.ToUpper(a.ArchiveSHA256)+"  "+a.Archive+"\n", 0644)
				case "no newline":
					put(t, checksum, strings.TrimSpace(string(read(t, checksum))), 0644)
				case "empty":
					put(t, checksum, "", 0644)
				case "short":
					put(t, checksum, "abc\n", 0644)
				default:
					name := checksum
					if mutation == "archive symlink" {
						name = archive
					} else if mutation == "manifest symlink" {
						name = filepath.Join(dir, "sodapop-"+testVersion+"-manifest.json")
					}
					if err := os.Rename(name, name+".real"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(name+".real", name); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
					if mutation == "manifest symlink" {
						if _, err := ReadManifest(name); err == nil {
							t.Fatal("symlink manifest accepted")
						}
						return
					}
				}
				assertRejectedUnchanged(t, dir, m)
			})
		}
		if runtime.GOOS == "windows" {
			return
		}
		for _, transform := range []func([]byte) []byte{
			func(data []byte) []byte { return data[:len(data)-10] },
			func(data []byte) []byte { data[0] ^= 0xff; return data },
			func(data []byte) []byte { return append(data, []byte("unexpected trailer")...) },
		} {
			dir, m := malformedArchive(t, platform, validEntries(platform), transform)
			assertRejectedUnchanged(t, dir, m)
		}
	}
	for _, transform := range []func([]byte) []byte{
		func(data []byte) []byte { return append(data, []byte("hidden")...) },
		func(data []byte) []byte { return append(data, data...) },
		func(data []byte) []byte { data[len(data)-8] ^= 0xff; return data },
	} {
		dir, m := malformedArchive(t, "linux/amd64", validEntries("linux/amd64"), transform)
		assertRejectedUnchanged(t, dir, m)
	}
}

func TestExtractionRefusesOutputClobberAndMissingParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix archive fixture coverage is covered on Unix runners")
	}
	dir, m := releaseFixture(t, "linux/amd64")
	parent := t.TempDir()
	output := filepath.Join(parent, "output")
	put(t, filepath.Join(output, "keep"), "untouched", 0600)
	if err := Extract(dir, m, "linux/amd64", output); err == nil {
		t.Fatal("nonempty output accepted")
	}

	if string(read(t, filepath.Join(output, "keep"))) != "untouched" {
		t.Fatal("output contents changed")
	}
	for _, bad := range []string{"", filepath.Join(parent, "missing", "child"), filepath.Join(output, "keep")} {
		if err := Extract(dir, m, "linux/amd64", bad); err == nil {
			t.Fatalf("bad output accepted: %s", bad)
		}
	}
	if err := Extract(dir, m, "", filepath.Join(parent, "new")); err == nil {
		t.Fatal("missing platform accepted")
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(output, link); err == nil {
		if err := Extract(dir, m, "linux/amd64", link); err == nil {
			t.Fatal("output symlink accepted")
		}
	}
}

func TestPayloadBoundsRejectBeforeReadingOrWriting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		size   int64
		change func(*payload)
	}{
		{"binary too large", maxBinary + 1, func(*payload) {}},
		{"negative size", -1, func(*payload) {}},
		{"aggregate limit", 1, func(p *payload) { p.total = maxPayload }},
		{"entry count", 1, func(p *payload) {
			for i := range maxEntries {
				p.seen[strconv.Itoa(i)] = true
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPayload(testVersion, "linux/amd64")
			tc.change(p)
			if err := p.entry(p.root+"/sodapop", 0755, tc.size, nil, nil); err == nil {
				t.Fatal("out-of-bound payload accepted")
			}
		})
	}
	p := newPayload(testVersion, "linux/amd64")
	if err := p.entry(p.root+"/LICENSE", 0644, maxText+1, nil, nil); err == nil {
		t.Fatal("oversized license accepted")
	}
	if err := p.entry(p.root+"/README.md", 0644, 10, strings.NewReader("short"), nil); err == nil {
		t.Fatal("short payload accepted")
	}
}

func TestVersionPlatformAndPinValidation(t *testing.T) {
	for _, version := range []string{"0.0.0", "1.2.3", "1.2.3-rc.0+build.01", testVersion} {
		if err := ValidateVersion(version); err != nil {
			t.Fatal(err)
		}
	}
	for _, version := range []string{"", "dev", "v1.2.3", "01.2.3", "1.2.3-01", "1.2", "1.2.3+", "../bad", strings.Repeat("1", 129)} {
		if err := ValidateVersion(version); err == nil {
			t.Fatalf("invalid version accepted: %q", version)
		}
	}
	wantPlatforms := []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"}
	if got := DefaultPlatforms(); !slices.Equal(got, wantPlatforms) {
		t.Fatalf("default platforms = %v, want %v", got, wantPlatforms)
	}
	for _, platform := range []string{"windows/amd64", "windows/arm64"} {
		if ArchiveName(testVersion, platform) != PackageName(testVersion, platform)+".zip" ||
			BinaryName(platform) != "sodapop.exe" {
			t.Fatalf("incorrect Windows distribution names for %s", platform)
		}
	}
	for _, set := range []string{"", "linux/amd64,", " linux/amd64", "linux/amd64,linux/amd64", "windows/386"} {
		if _, err := ParsePlatforms(set); err == nil {
			t.Fatalf("invalid set accepted: %q", set)
		}
	}
	for _, source := range []string{
		"bad Go source",
		`package runtimebundle; var Version = "1.0.83"`,
		`package runtimebundle; const Version = "bad"`,
		`package runtimebundle; const Version = "1.0." + "83"`,
		`package runtimebundle; const (Version = "1.0.83"; Version = "1.0.83")`,
		`package runtimebundle; const (Other = "1.0.83"; Version)`,
	} {
		repo := pins(t)
		put(t, filepath.Join(repo, "internal/runtimebundle/version.go"), source, 0644)
		if _, _, err := ReadPins(repo); err == nil {
			t.Fatal("invalid pins accepted")
		}
	}
	repo := pins(t)
	put(t, filepath.Join(repo, "go.mod"), "require github.com/github/copilot-sdk/go v9.9.9\n", 0644)
	if _, _, err := ReadPins(repo); err == nil {
		t.Fatal("SDK pin mismatch accepted")
	}
	if _, _, err := ReadPins(t.TempDir()); err == nil {
		t.Fatal("missing pins accepted")
	}
	if _, err := Generate(t.TempDir(), "bad", strings.Repeat("a", 40), pins(t), DefaultPlatforms()); err == nil {
		t.Fatal("bad generate version accepted")
	}
	if _, err := Generate(t.TempDir(), testVersion, "short", pins(t), DefaultPlatforms()); err == nil {
		t.Fatal("bad generate commit accepted")
	}
}

func TestArchiveRejectsBadStagingAndExistingOutputs(t *testing.T) {
	platforms := []string{"linux/amd64", "windows/amd64", "windows/arm64"}
	if runtime.GOOS == "windows" {
		platforms = []string{"windows/amd64", "windows/arm64"}
	}
	for _, platform := range platforms {
		stage := stageFixture(t, platform)
		dir := t.TempDir()
		artifact, err := Archive(stage, dir, testVersion, platform)
		if err != nil {
			t.Fatal(err)
		}
		before := read(t, filepath.Join(dir, artifact.Archive))
		if _, err := Archive(stage, dir, testVersion, platform); err == nil {
			t.Fatal("existing archive overwritten")
		}
		if !bytes.Equal(before, read(t, filepath.Join(dir, artifact.Archive))) {
			t.Fatal("existing archive changed")
		}
		for _, mutation := range []string{"private", "missing", "empty", "symlink"} {
			stage := stageFixture(t, platform)
			switch mutation {
			case "private":
				put(t, filepath.Join(stage, ".env"), "private", 0600)
			case "missing":
				if err := os.Remove(filepath.Join(stage, "LICENSE")); err != nil {
					t.Fatal(err)
				}
			case "empty":
				put(t, filepath.Join(stage, "LICENSE"), "", 0644)
			case "symlink":
				if err := os.Symlink("LICENSE", filepath.Join(stage, "extra")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			output := t.TempDir()
			if _, err := Archive(stage, output, testVersion, platform); err == nil {
				t.Fatalf("bad staging accepted: %s", mutation)
			}
			entries, err := os.ReadDir(output)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed archive left output: %v, %v", entries, err)
			}
		}
	}
	for _, values := range [][3]string{{"bad", "linux/amd64", ""}, {testVersion, "invalid", ""}, {testVersion, "linux/amd64", "wrong-name"}} {
		if _, err := Archive(values[2], t.TempDir(), values[0], values[1]); err == nil {
			t.Fatal("invalid archive arguments accepted")
		}
	}
}
