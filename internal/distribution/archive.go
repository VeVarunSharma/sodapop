package distribution

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxArchive = int64(2 << 30)
	maxPayload = int64(2 << 30)
	maxBinary  = int64(1 << 30)
	maxText    = int64(16 << 20)
	maxEntries = 4096
)

var componentPattern = regexp.MustCompile(`^[A-Za-z0-9_@+][A-Za-z0-9_.@+-]*$`)

var documentation = []string{
	"LICENSE", "README.md", "THIRD_PARTY_NOTICES.md",
	"docs/authentication.md", "docs/architecture.md", "docs/live-qualification.md",
	"docs/branding.md", "docs/vhs/README.md",
	"images/sodapop-title.gif", "images/sodapop-title.png",
	"docs/assets/demos/overview.gif", "docs/assets/demos/overview.png",
	"docs/assets/demos/commands.gif", "docs/assets/demos/commands.png",
	"docs/assets/demos/themes.gif", "docs/assets/demos/themes.png",
	"docs/assets/demos/diff.gif", "docs/assets/demos/diff.png",
}

type payload struct {
	root       string
	binary     string
	allowed    map[string]bool
	dirs       map[string]bool
	seen       map[string]bool
	files      map[string]bool
	parents    map[string]bool
	total      int64
	dependency bool
	binaryHash string
}

func newPayload(version, platform string) *payload {
	p := &payload{
		root: PackageName(version, platform), binary: BinaryName(platform),
		allowed: make(map[string]bool), dirs: map[string]bool{".": true, "LICENSES": true},
		seen: make(map[string]bool), files: make(map[string]bool), parents: make(map[string]bool),
	}
	for _, name := range append(append([]string{}, documentation...), p.binary, "LICENSES/copilot-runtime.license") {
		p.allowed[name] = true
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			p.dirs[dir] = true
		}
	}
	return p
}

func safeArchivePath(name string, dir bool) (string, error) {
	clean := strings.TrimSuffix(name, "/")
	if clean == "" || path.Clean(clean) != clean || strings.Contains(name, `\`) || (!dir && clean != name) {
		return "", fmt.Errorf("unsafe archive entry %q", name)
	}
	for _, part := range strings.Split(clean, "/") {
		if !componentPattern.MatchString(part) || strings.HasSuffix(part, ".") {
			return "", fmt.Errorf("unsafe archive entry %q", name)
		}
		base, _, _ := strings.Cut(strings.ToUpper(part), ".")
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return "", fmt.Errorf("reserved archive entry %q", name)
		}
	}
	return clean, nil
}

func noticeName(name string) bool {
	upper := strings.ToUpper(name)
	return strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "LICENCE") ||
		strings.HasPrefix(upper, "COPYING") || strings.HasPrefix(upper, "NOTICE") ||
		strings.HasSuffix(upper, ".LICENSE") || strings.HasSuffix(upper, ".NOTICE")
}

func dependencyPath(name string, dir bool) bool {
	parts := strings.Split(name, "/")
	if parts[0] != "LICENSES" {
		return false
	}
	if dir {
		return len(parts) == 2 && strings.Contains(parts[1], "@")
	}
	return (len(parts) == 2 || (len(parts) == 3 && strings.Contains(parts[1], "@"))) && noticeName(parts[len(parts)-1])
}

func (p *payload) entry(name string, mode fs.FileMode, size int64, reader io.Reader, output *os.Root) error {
	dir := mode.IsDir()
	if !dir && !mode.IsRegular() || mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("archive entry %q is not a plain regular file or directory", name)
	}
	clean, err := safeArchivePath(name, dir)
	if err != nil {
		return err
	}
	key := strings.ToLower(clean)
	if p.seen[key] || len(p.seen) >= maxEntries {
		return fmt.Errorf("duplicate archive entry or entry limit exceeded: %q", name)
	}
	p.seen[key] = true
	relative := "."
	if clean != p.root {
		var ok bool
		relative, ok = strings.CutPrefix(clean, p.root+"/")
		if !ok {
			return fmt.Errorf("archive entry outside package root: %q", name)
		}
	}
	if dir {
		if size != 0 || (!p.dirs[relative] && !dependencyPath(relative, true)) {
			return fmt.Errorf("unexpected directory %q", name)
		}
		return nil
	}
	if !p.allowed[relative] && !dependencyPath(relative, false) {
		return fmt.Errorf("unexpected file %q", name)
	}
	limit := maxText
	if relative == p.binary {
		limit = maxBinary
		if p.binary != "sodapop.exe" && mode.Perm()&0100 == 0 {
			return fmt.Errorf("packaged binary is not executable")
		}
	}
	if size <= 0 || size > limit || p.total > maxPayload-size {
		return fmt.Errorf("invalid payload size for %q", name)
	}
	p.total += size
	if p.parents[strings.ToLower(relative)] {
		return fmt.Errorf("archive file/directory collision: %q", name)
	}
	for parent := path.Dir(relative); parent != "."; parent = path.Dir(parent) {
		if p.files[strings.ToLower(parent)] {
			return fmt.Errorf("archive file/directory collision: %q", name)
		}
		p.parents[strings.ToLower(parent)] = true
	}
	p.files[strings.ToLower(relative)] = true
	var destination *os.File
	var writer io.Writer = io.Discard
	if output != nil {
		if err := output.MkdirAll(filepath.FromSlash(path.Dir(clean)), 0755); err != nil {
			return err
		}
		permissions := fs.FileMode(0644)
		if relative == p.binary {
			permissions = 0755
		}
		destination, err = output.OpenFile(filepath.FromSlash(clean), os.O_WRONLY|os.O_CREATE|os.O_EXCL, permissions)
		if err != nil {
			return err
		}
		writer = destination
	}
	hash := sha256.New()
	content := &textContent{}
	written, copyErr := io.Copy(io.MultiWriter(hash, writer, content), io.LimitReader(reader, size+1))
	var closeErr error
	if destination != nil {
		closeErr = destination.Close()
	}
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	if written != size {
		return fmt.Errorf("payload size mismatch for %q", name)
	}
	if (relative == "LICENSE" || relative == "THIRD_PARTY_NOTICES.md" || strings.HasPrefix(relative, "LICENSES/")) && !content.nonblank {
		return fmt.Errorf("empty license/notices text in %q", name)
	}
	if relative == p.binary {
		p.binaryHash = hex.EncodeToString(hash.Sum(nil))
	}
	if strings.HasPrefix(relative, "LICENSES/") && relative != "LICENSES/copilot-runtime.license" {
		p.dependency = true
	}
	return nil
}

func (p *payload) finish() error {
	for _, name := range []string{p.binary, "LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSES/copilot-runtime.license"} {
		if !p.files[strings.ToLower(name)] {
			return fmt.Errorf("missing required payload %s", name)
		}
	}
	if !p.dependency {
		return fmt.Errorf("missing dependency license/notices payload")
	}
	return nil
}

type textContent struct{ nonblank bool }

func (content *textContent) Write(data []byte) (int, error) {
	if !content.nonblank {
		content.nonblank = len(bytes.Trim(data, " \r\n\t\x00")) != 0
	}
	return len(data), nil
}

func parseChecksum(data []byte, archive string) (string, error) {
	text := strings.TrimSuffix(string(data), "\n")
	if len(data) == 0 || data[len(data)-1] != '\n' || strings.ContainsAny(text, "\r\n") ||
		len(text) != 66+len(archive) || text[64:66] != "  " || text[66:] != archive ||
		!digestPattern.MatchString(text[:64]) {
		return "", fmt.Errorf("invalid checksum file for %s: expected one SHA-256 and archive basename", archive)
	}
	return text[:64], nil
}

func inspectArchive(dir, version string, artifact Artifact, output *os.Root) (string, string, error) {
	checksum, err := readRegular(filepath.Join(dir, artifact.Archive+".sha256"), 1024)
	if err != nil {
		return "", "", fmt.Errorf("archive checksum: %w", err)
	}
	expected, err := parseChecksum(checksum, artifact.Archive)
	if err != nil {
		return "", "", err
	}
	if artifact.ArchiveSHA256 != "" && expected != artifact.ArchiveSHA256 {
		return "", "", fmt.Errorf("archive checksum differs from manifest")
	}
	file, err := openRegular(filepath.Join(dir, artifact.Archive), maxArchive)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxArchive+1)); err != nil {
		return "", "", err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return "", "", fmt.Errorf("archive checksum mismatch for %s", artifact.Archive)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	p := newPayload(version, artifact.Platform)
	if err := walkArchive(file, artifact.Platform, p, output); err != nil {
		return "", "", err
	}
	if err := p.finish(); err != nil {
		return "", "", err
	}
	if artifact.BinarySHA256 != "" && artifact.BinarySHA256 != p.binaryHash {
		return "", "", fmt.Errorf("binary SHA-256 mismatch for %s", artifact.Platform)
	}
	// Check the same open file again before accepting staged extraction, guarding
	// against in-place changes between hashing and reading archive entries.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	hash.Reset()
	if _, err := io.Copy(hash, io.LimitReader(file, maxArchive+1)); err != nil {
		return "", "", err
	}
	if hex.EncodeToString(hash.Sum(nil)) != actual {
		return "", "", fmt.Errorf("archive changed during verification")
	}
	return actual, p.binaryHash, nil
}

func walkArchive(file *os.File, platform string, p *payload, output *os.Root) error {
	if BinaryName(platform) == "sodapop.exe" {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		reader, err := zip.NewReader(file, info.Size())
		if err != nil {
			return err
		}
		if len(reader.File) > maxEntries {
			return fmt.Errorf("archive entry limit exceeded")
		}
		var end [22]byte
		if _, err := file.ReadAt(end[:], info.Size()-int64(len(end))); err != nil {
			return err
		}
		if reader.Comment != "" || !bytes.Equal(end[:4], []byte{'P', 'K', 5, 6}) || end[20] != 0 || end[21] != 0 {
			return fmt.Errorf("unexpected data after ZIP directory")
		}
		for _, entry := range reader.File {
			if entry.UncompressedSize64 > uint64(maxBinary) || entry.Flags&1 != 0 ||
				entry.Mode().IsDir() != strings.HasSuffix(entry.Name, "/") {
				return fmt.Errorf("invalid ZIP entry %q", entry.Name)
			}
			stream, err := entry.Open()
			if err != nil {
				return err
			}
			err = p.entry(entry.Name, entry.Mode(), int64(entry.UncompressedSize64), stream, output)
			if err == nil && entry.Mode().IsDir() {
				_, err = io.Copy(io.Discard, stream)
			}
			if err := errors.Join(err, stream.Close()); err != nil {
				return err
			}
		}
		return nil
	}
	buffer := bufio.NewReader(file)
	compressed, err := gzip.NewReader(buffer)
	if err != nil {
		return err
	}
	defer compressed.Close()
	compressed.Multistream(false)
	limited := &io.LimitedReader{R: compressed, N: maxPayload + (8 << 20)}
	reader := tar.NewReader(limited)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir ||
			header.Linkname != "" || header.Mode & ^int64(0777) != 0 {
			return fmt.Errorf("archive entry %q is not a plain regular file or directory", header.Name)
		}
		for key := range header.PAXRecords {
			if key != "path" && key != "mtime" && key != "atime" && key != "ctime" && key != "size" {
				return fmt.Errorf("unsupported archive metadata %q", key)
			}
		}
		if err := p.entry(header.Name, header.FileInfo().Mode(), header.Size, reader, output); err != nil {
			return err
		}
	}
	// tar stops at its end blocks. Read the gzip trailer as well; otherwise a
	// corrupt CRC or a concatenated hidden archive can pass tar validation.
	padding, err := io.ReadAll(io.LimitReader(limited, 1<<20+1))
	if err != nil {
		return err
	}
	if len(padding) > 1<<20 || len(strings.Trim(string(padding), "\x00")) != 0 || limited.N <= 0 {
		return fmt.Errorf("unexpected data after tar payload")
	}
	if _, err := buffer.ReadByte(); err != io.EOF {
		return fmt.Errorf("unexpected data after gzip stream")
	}
	return nil
}

func Verify(dir string, m Manifest, platform string, expected []string) error {
	if err := ValidateManifest(m, expected); err != nil {
		return err
	}
	selected := false
	for _, artifact := range m.Artifacts {
		if platform != "" && artifact.Platform != platform {
			continue
		}
		selected = true
		if _, _, err := inspectArchive(dir, m.Version, artifact, nil); err != nil {
			return fmt.Errorf("%s: %w", artifact.Platform, err)
		}
	}
	if !selected {
		return fmt.Errorf("manifest does not contain requested platform %q", platform)
	}
	return nil
}
