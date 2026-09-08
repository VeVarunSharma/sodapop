// windowsctl prepares Windows delivery metadata without rebuilding release bytes.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const repository = "https://github.com/VeVarunSharma/sodapop"
const upgradeCode = "972F78B3-B6A5-5422-AABD-CA10FE7E6241"
const manifestVersion = "1.9.0"

var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var hex64RE = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
var commitRE = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
var idRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,31}(\.[A-Za-z][A-Za-z0-9]{0,31}){1,7}$`)
var segmentRE = regexp.MustCompile(`^[A-Za-z0-9_@+.-]+$`)

type artifact struct {
	Platform      string `json:"platform"`
	Archive       string `json:"archive"`
	ArchiveSHA256 string `json:"archive_sha256"`
	BinarySHA256  string `json:"binary_sha256"`
}

type release struct {
	SchemaVersion  int        `json:"schema_version"`
	Version        string     `json:"version"`
	Commit         string     `json:"commit"`
	SDKVersion     string     `json:"copilot_sdk_version"`
	RuntimeVersion string     `json:"copilot_runtime_version"`
	Artifacts      []artifact `json:"artifacts"`
}

type options struct {
	command, dir, manifest, output, tool, packageID, tag string
}

// There is intentionally no ARM64 entry until native release evidence exists.
var architectures = map[string]struct{ winget, scoop string }{
	"windows/amd64": {"x64", "64bit"},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "windowsctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected manifests, prepare-msi, or portable")
	}
	o := options{command: args[0]}
	f := flag.NewFlagSet(o.command, flag.ContinueOnError)
	f.StringVar(&o.dir, "dir", "", "release archive directory")
	f.StringVar(&o.manifest, "manifest", "", "trusted final schema-1 manifest")
	f.StringVar(&o.output, "output", "", "new output directory (existing paths are rejected)")
	f.StringVar(&o.tool, "releasectl", os.Getenv("SODAPOP_RELEASECTL"), "native shared verifier executable")
	f.StringVar(&o.packageID, "package-id", "VeVarunSharma.Sodapop", "WinGet identifier")
	f.StringVar(&o.tag, "tag", "", "exact public tag (default vVERSION)")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || (o.command != "manifests" && o.command != "prepare-msi" && o.command != "portable") {
		return errors.New("unexpected command or positional arguments")
	}
	if o.dir == "" || o.manifest == "" || o.output == "" || o.tool == "" {
		return errors.New("--dir, --manifest, --output and --releasectl (or SODAPOP_RELEASECTL) are required")
	}
	if len(o.packageID) > 128 || !idRE.MatchString(o.packageID) {
		return errors.New("invalid WinGet package identifier")
	}
	var err error
	for _, p := range []*string{&o.dir, &o.manifest, &o.output, &o.tool} {
		*p, err = filepath.Abs(*p)
		if err != nil {
			return err
		}
	}
	if err := newDestination(o.output); err != nil {
		return err
	}
	r, a, err := readRelease(o.manifest)
	if err != nil {
		return err
	}
	if o.tag == "" {
		o.tag = "v" + r.Version
	}
	if o.tag != "v"+r.Version {
		return errors.New("--tag must be the exact canonical vVERSION tag")
	}
	if o.command == "prepare-msi" {
		if err := msiVersion(r.Version); err != nil {
			return err
		}
	}
	if err := invokeVerifier(o, "verify", ""); err != nil {
		return err
	}
	if o.command == "portable" {
		// The shared extractor owns path/link/root and archive/binary verification.
		return invokeVerifier(o, "extract", o.output)
	}
	if err := os.Mkdir(o.output, 0755); err != nil {
		return err
	}
	if o.command == "manifests" {
		return writeManifests(o, r, a)
	}
	return prepareMSI(o, r, a)
}

func newDestination(destination string) error {
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return fmt.Errorf("output already exists (refusing stale output): %s", destination)
	}
	// Require an existing, link-free parent: never create through junctions or
	// symlinks, or interpret a missing parent as permission to build a wide tree.
	for dir := filepath.Dir(destination); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("output parent is not a real directory: %s", dir)
		}
		if filepath.Dir(dir) == dir {
			return nil
		}
	}
}

func readRelease(filename string) (release, artifact, error) {
	var r release
	var selected artifact
	info, err := os.Lstat(filename)
	if err != nil {
		return r, selected, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return r, selected, errors.New("release manifest must be a regular file no larger than 1 MiB")
	}
	f, err := os.Open(filename)
	if err != nil {
		return r, selected, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return r, selected, err
	}
	if len(data) > 1<<20 {
		return r, selected, errors.New("release manifest exceeds 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, selected, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return r, selected, errors.New("trailing data in release manifest")
	}
	if r.SchemaVersion != 1 || !validVersion(r.Version) || !commitRE.MatchString(r.Commit) ||
		!validVersion(strings.TrimPrefix(r.SDKVersion, "v")) || !validVersion(strings.TrimPrefix(r.RuntimeVersion, "v")) {
		return r, selected, errors.New("invalid release schema, version, commit, or upstream versions")
	}
	seen := map[string]bool{}
	for _, a := range r.Artifacts {
		if seen[a.Platform] || !hex64RE.MatchString(a.ArchiveSHA256) || !hex64RE.MatchString(a.BinarySHA256) {
			return r, selected, errors.New("duplicate platform or invalid release hash")
		}
		seen[a.Platform] = true
		if a.Platform == "windows/amd64" {
			if a.Archive != archiveRoot(r.Version)+".zip" {
				return r, selected, errors.New("Windows archive name does not match release version and platform")
			}
			selected = a
		}
	}
	if selected.Platform == "" {
		return r, selected, errors.New("missing windows/amd64 release artifact")
	}
	return r, selected, nil
}

func validVersion(v string) bool {
	if len(v) > 64 || !versionRE.MatchString(v) {
		return false
	}
	core, _, _ := strings.Cut(v, "+")
	_, pre, ok := strings.Cut(core, "-")
	if ok {
		for _, part := range strings.Split(pre, ".") {
			if len(part) > 1 && part[0] == '0' && strings.Trim(part, "0123456789") == "" {
				return false
			}
		}
	}
	return true
}

func msiVersion(v string) error {
	if !validVersion(v) || strings.ContainsAny(v, "-+") {
		return errors.New("MSI requires numeric MAJOR.MINOR.PATCH; prereleases/build metadata are unsupported, never truncated")
	}
	for i, part := range strings.Split(v, ".") {
		n, err := strconv.ParseUint(part, 10, 16)
		max := uint64(255)
		if i == 2 {
			max = 65535
		}
		if err != nil || n > max {
			return errors.New("MSI version exceeds 255.255.65535")
		}
	}
	return nil
}

func archiveRoot(version string) string { return "sodapop-" + version + "-windows-amd64" }

func invokeVerifier(o options, action, output string) error {
	args := []string{action, "--dir", o.dir, "--manifest", o.manifest, "--platform", "windows/amd64"}
	if output != "" {
		args = append(args, "--output", output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.tool, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("shared release %s failed: %w", action, err)
	}
	return nil
}

func yamlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func writeNew(filename string, data []byte) error {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	return errors.Join(writeErr, f.Close())
}

func writeJSON(filename string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeNew(filename, append(data, '\n'))
}

func writeManifests(o options, r release, a artifact) error {
	parts := strings.Split(o.packageID, ".")
	rel := append([]string{"winget", "manifests", strings.ToLower(o.packageID[:1])}, parts...)
	rel = append(rel, r.Version)
	wingetDir := filepath.Join(append([]string{o.output}, rel...)...)
	scoopDir := filepath.Join(o.output, "scoop", "bucket")
	for _, dir := range []string{wingetDir, scoopDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	common := "PackageIdentifier: " + yamlString(o.packageID) + "\nPackageVersion: " + yamlString(r.Version) + "\n"
	url := repository + "/releases/download/" + o.tag + "/" + a.Archive
	blob := repository + "/blob/" + o.tag + "/"
	arch := architectures[a.Platform]
	files := map[string]string{
		"": common + "DefaultLocale: en-US\nManifestType: version\n",
		".installer": common + "InstallerType: zip\nNestedInstallerType: portable\n" +
			"NestedInstallerFiles:\n  - RelativeFilePath: " + yamlString(archiveRoot(r.Version)+`\sodapop.exe`) +
			"\n    PortableCommandAlias: sodapop\nScope: user\nElevationRequirement: elevationProhibited\n" +
			"UpgradeBehavior: uninstallPrevious\nCommands:\n  - sodapop\n" +
			"UnsupportedOSArchitectures:\n  - x86\n  - arm\n  - arm64\n" +
			"Installers:\n  - Architecture: " + arch.winget + "\n    InstallerUrl: " + yamlString(url) +
			"\n    InstallerSha256: " + yamlString(strings.ToUpper(a.ArchiveSHA256)) + "\nManifestType: installer\n",
		".locale.en-US": common + "PackageLocale: en-US\nPublisher: VeVarunSharma\n" +
			"PublisherUrl: " + yamlString(repository) + "\nPackageName: Sodapop\nPackageUrl: 'https://sodapop.sh'\n" +
			"License: 'MIT (Sodapop); separate bundled runtime and dependency terms'\nLicenseUrl: " + yamlString(blob+"LICENSE") +
			"\nCopyright: 'Copyright holders retain their respective rights; see included notices.'\n" +
			"ShortDescription: 'Terminal coding assistant backed by the official GitHub Copilot SDK.'\n" +
			"Moniker: sodapop\nDocumentations:\n  - DocumentLabel: Third-party notices and runtime terms\n    DocumentUrl: " +
			yamlString(blob+"THIRD_PARTY_NOTICES.md") + "\nManifestType: defaultLocale\n",
	}
	for suffix, body := range files {
		schema := "version"
		if suffix == ".installer" {
			schema = "installer"
		} else if suffix != "" {
			schema = "defaultLocale"
		}
		body = "# yaml-language-server: $schema=https://aka.ms/winget-manifest." + schema + "." + manifestVersion + ".schema.json\n" +
			body + "ManifestVersion: " + manifestVersion + "\n"
		if err := writeNew(filepath.Join(wingetDir, o.packageID+suffix+".yaml"), []byte(body)); err != nil {
			return err
		}
	}
	scoop := map[string]any{
		"version":     r.Version,
		"description": "Terminal coding assistant backed by the official GitHub Copilot SDK.",
		"homepage":    "https://sodapop.sh",
		"license":     map[string]string{"identifier": "Proprietary", "url": blob + "THIRD_PARTY_NOTICES.md"},
		"architecture": map[string]any{arch.scoop: map[string]string{
			"url": url, "hash": strings.ToLower(a.ArchiveSHA256), "extract_dir": archiveRoot(r.Version),
		}},
		"bin": "sodapop.exe",
		"notes": []string{
			"Sodapop source is MIT; the bundled Copilot runtime has separate terms. See included LICENSE, THIRD_PARTY_NOTICES.md and LICENSES.",
			blob + "THIRD_PARTY_NOTICES.md",
		},
	}
	return writeJSON(filepath.Join(scoopDir, "sodapop.json"), scoop)
}

type stagedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type msiInput struct {
	Version      string       `json:"version"`
	ProductCode  string       `json:"product_code"`
	UpgradeCode  string       `json:"upgrade_code"`
	Archive      string       `json:"archive"`
	ArchiveHash  string       `json:"archive_sha256"`
	BinaryHash   string       `json:"binary_sha256"`
	SigningState string       `json:"signing_state"`
	Files        []stagedFile `json:"files"`
}

func prepareMSI(o options, r release, a artifact) (result error) {
	extracted, err := os.MkdirTemp(o.output, ".verified-extract-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(extracted)) }()
	if err := invokeVerifier(o, "extract", extracted); err != nil {
		return err
	}
	root := filepath.Join(extracted, archiveRoot(r.Version))
	files, err := payloadFiles(root)
	if err != nil {
		return err
	}
	payload := filepath.Join(o.output, "payload")
	if err := os.Mkdir(payload, 0755); err != nil {
		return err
	}
	input := msiInput{
		Version: r.Version, ProductCode: stableGUID("product:windows/amd64:" + r.Version),
		UpgradeCode: upgradeCode, Archive: a.Archive, ArchiveHash: a.ArchiveSHA256,
		BinaryHash: a.BinarySHA256, SigningState: "unsigned-candidate",
	}
	for _, relative := range files {
		source := filepath.Join(root, filepath.FromSlash(relative))
		destination := filepath.Join(payload, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		hash, err := copyPayloadFile(source, destination)
		if err != nil {
			return err
		}
		if relative == "sodapop.exe" && !strings.EqualFold(hash, a.BinarySHA256) {
			return errors.New("staged binary differs from verified manifest")
		}
		input.Files = append(input.Files, stagedFile{relative, hash})
	}
	if err := writeNew(filepath.Join(o.output, "Payload.wxs"), []byte(payloadWXS(files))); err != nil {
		return err
	}
	return writeJSON(filepath.Join(o.output, "msi-input.json"), input)
}

func copyPayloadFile(source, destination string) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, h), in)
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func payloadFiles(root string) ([]string, error) {
	required := map[string]bool{
		"sodapop.exe": false, "README.md": false, "LICENSE": false,
		"THIRD_PARTY_NOTICES.md": false, "LICENSES/copilot-runtime.license": false,
	}
	var files []string
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("payload link rejected: %s", filename)
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if err := safePayloadPath(relative); err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == "LICENSES" || strings.HasPrefix(relative, "LICENSES/") {
				return nil
			}
			// Documentation/media already passed the shared archive allowlist,
			// but are deliberately not MSI payload.
			if relative == "docs" || relative == "images" {
				return filepath.SkipDir
			}
			return fmt.Errorf("unexpected payload directory: %s", relative)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular payload: %s", relative)
		}
		if _, ok := required[relative]; ok {
			required[relative] = true
		} else if !strings.HasPrefix(relative, "LICENSES/") || !isNotice(path.Base(relative)) {
			return fmt.Errorf("unexpected MSI payload: %s", relative)
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for name, found := range required {
		if !found {
			return nil, fmt.Errorf("required payload missing: %s", name)
		}
	}
	sort.Strings(files)
	return files, nil
}

func isNotice(name string) bool {
	n := strings.ToUpper(name)
	for _, suffix := range []string{".LICENSE", ".NOTICE"} {
		if len(n) > len(suffix) && strings.HasSuffix(n, suffix) {
			return true
		}
	}
	for _, prefix := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE"} {
		if n == prefix || strings.HasPrefix(n, prefix+".") || strings.HasPrefix(n, prefix+"-") {
			ext := strings.ToLower(path.Ext(name))
			switch ext {
			case "", ".md", ".txt", ".license", ".bsd", ".apache", ".mit", ".unicode", ".lesser", ".gpl", ".lgpl":
				return true
			}
			return false
		}
	}
	return false
}

func safePayloadPath(p string) error {
	if len(p) > 180 || strings.Contains(p, "\\") {
		return fmt.Errorf("unsafe/overlong MSI payload path: %s", p)
	}
	for _, segment := range strings.Split(p, "/") {
		base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
		if !segmentRE.MatchString(segment) || segment == "." || segment == ".." ||
			strings.HasSuffix(segment, ".") || base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return fmt.Errorf("unsafe MSI payload path: %s", p)
		}
	}
	return nil
}

func stableGUID(name string) string {
	h := sha256.Sum256([]byte("github.com/VeVarunSharma/sodapop:per-user-msi:" + name))
	h[6] = (h[6] & 0x0f) | 0x80 // RFC 9562 custom, deterministic UUIDv8.
	h[8] = (h[8] & 0x3f) | 0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func wixID(prefix, value string) string {
	h := sha256.Sum256([]byte(strings.ToLower(value)))
	return prefix + hex.EncodeToString(h[:12])
}

func xmlAttribute(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func directoryID(dir string) string {
	if dir == "." {
		return "INSTALLFOLDER"
	}
	return wixID("D", dir)
}

func payloadWXS(files []string) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<Wix xmlns=\"http://wixtoolset.org/schemas/v4/wxs\">\n<Fragment>\n")
	directories := map[string]bool{}
	for _, file := range files {
		for dir := path.Dir(file); dir != "."; dir = path.Dir(dir) {
			directories[dir] = true
		}
	}
	dirs := make([]string, 0, len(directories))
	for dir := range directories {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		fmt.Fprintf(&b, "<DirectoryRef Id=\"%s\"><Directory Id=\"%s\" Name=\"%s\" /></DirectoryRef>\n",
			directoryID(path.Dir(dir)), directoryID(dir), xmlAttribute(path.Base(dir)))
	}
	b.WriteString("<ComponentGroup Id=\"Payload\">\n")
	for _, dir := range dirs {
		id := wixID("R", dir)
		fmt.Fprintf(&b, "<Component Id=\"%s\" Directory=\"%s\" Guid=\"%s\">\n", id, directoryID(dir), stableGUID("directory:"+dir))
		fmt.Fprintf(&b, "<RegistryValue Root=\"HKCU\" Key=\"Software\\Sodapop\\Installer\" Name=\"%s\" Type=\"integer\" Value=\"1\" KeyPath=\"yes\" />\n", id)
		fmt.Fprintf(&b, "<RemoveFolder Id=\"%s\" On=\"uninstall\" />\n</Component>\n", id)
	}
	for _, file := range files {
		id := wixID("C", file)
		fmt.Fprintf(&b, "<Component Id=\"%s\" Directory=\"%s\" Guid=\"%s\">\n", id, directoryID(path.Dir(file)), stableGUID("file:"+file))
		fmt.Fprintf(&b, "<RegistryValue Root=\"HKCU\" Key=\"Software\\Sodapop\\Installer\" Name=\"%s\" Type=\"integer\" Value=\"1\" KeyPath=\"yes\" />\n", id)
		fmt.Fprintf(&b, "<File Id=\"%s\" Source=\"$(PayloadDir)\\%s\" />\n</Component>\n", wixID("F", file), xmlAttribute(strings.ReplaceAll(file, "/", "\\")))
	}
	b.WriteString("</ComponentGroup>\n</Fragment>\n</Wix>\n")
	return b.String()
}
