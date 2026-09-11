package distribution

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Archive packages an already explicit, freshly staged payload. Existing release
// assets are never replaced, including dangling symlinks.
func Archive(stage, dir, version, platform string) (Artifact, error) {
	artifact := Artifact{Platform: platform, Archive: ArchiveName(version, platform)}
	if err := ValidateVersion(version); err != nil {
		return artifact, err
	}
	if err := ValidatePlatform(platform); err != nil {
		return artifact, err
	}
	if filepath.Base(stage) != PackageName(version, platform) {
		return artifact, fmt.Errorf("staging directory must have the package-root name")
	}
	if info, err := os.Lstat(stage); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return artifact, fmt.Errorf("staging must be a real directory")
	}
	name := filepath.Join(dir, artifact.Archive)
	for _, output := range []string{name, name + ".sha256"} {
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			return artifact, fmt.Errorf("refusing existing or inaccessible output %s", output)
		}
	}
	temporary, err := os.CreateTemp(dir, ".sodapop-archive-")
	if err != nil {
		return artifact, err
	}
	defer os.Remove(temporary.Name())
	hash := sha256.New()
	writer := io.MultiWriter(temporary, hash)
	p := newPayload(version, platform)
	var tarWriter *tar.Writer
	var gzipWriter *gzip.Writer
	var zipWriter *zip.Writer
	if BinaryName(platform) == "sodapop.exe" {
		zipWriter = zip.NewWriter(writer)
	} else {
		gzipWriter = gzip.NewWriter(writer)
		tarWriter = tar.NewWriter(gzipWriter)
	}
	err = filepath.WalkDir(stage, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(filepath.Dir(stage), name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		size := info.Size()
		if info.IsDir() {
			size = 0
			relative += "/"
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("staged entry must be regular: %s", name)
		}
		var input *os.File
		var reader io.Reader
		if !info.IsDir() {
			input, err = openRegular(name, maxBinary)
			if err != nil {
				return err
			}
			defer input.Close()
			reader = input
		}
		// Validate source entries before writing their headers.
		if err := p.entry(relative, info.Mode(), size, reader, nil); err != nil {
			return err
		}
		mode := fs.FileMode(0644)
		if info.IsDir() {
			mode = fs.ModeDir | 0755
		} else if filepath.Base(name) == BinaryName(platform) && filepath.Dir(name) == filepath.Clean(stage) {
			mode = 0755
		}
		var stream io.Writer
		if zipWriter != nil {
			header := &zip.FileHeader{Name: relative, Method: zip.Deflate}
			header.SetMode(mode)
			if info.IsDir() {
				header.Method = zip.Store
			}
			stream, err = zipWriter.CreateHeader(header)
		} else {
			header := &tar.Header{Name: relative, Mode: int64(mode.Perm()), Size: size, Typeflag: tar.TypeReg}
			if info.IsDir() {
				header.Typeflag = tar.TypeDir
			}
			err = tarWriter.WriteHeader(header)
			stream = tarWriter
		}
		if err != nil {
			return err
		}
		if input != nil {
			if _, err := input.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if _, err := io.CopyN(stream, input, size); err != nil {
				return err
			}
		}
		return nil
	})
	if zipWriter != nil {
		err = errors.Join(err, zipWriter.Close())
	} else {
		err = errors.Join(err, tarWriter.Close(), gzipWriter.Close())
	}
	err = errors.Join(err, temporary.Close())
	if err != nil {
		return artifact, err
	}
	if err := p.finish(); err != nil {
		return artifact, err
	}
	artifact.ArchiveSHA256 = hex.EncodeToString(hash.Sum(nil))
	artifact.BinarySHA256 = p.binaryHash
	// Link publishes without replacing any existing path, unlike os.Rename.
	if err := os.Link(temporary.Name(), name); err != nil {
		return artifact, err
	}
	checksum := []byte(artifact.ArchiveSHA256 + "  " + artifact.Archive + "\n")
	if err := writeExclusive(name+".sha256", checksum, 0644); err != nil {
		os.Remove(name)
		return artifact, err
	}
	// Validate the final compressed bytes, not just the source staging tree.
	if _, _, err := inspectArchive(dir, version, artifact, nil); err != nil {
		os.Remove(name + ".sha256")
		os.Remove(name)
		return artifact, err
	}
	return artifact, nil
}
