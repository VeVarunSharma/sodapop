package distribution

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func Extract(dir string, m Manifest, platform, output string) error {
	if platform == "" || output == "" {
		return fmt.Errorf("extract requires platform and output")
	}
	if err := Verify(dir, m, platform, nil); err != nil {
		return err
	}
	absolute, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	if err := plainDirectory(parent); err != nil {
		return err
	}
	exists, err := emptyOutput(absolute)
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".sodapop-extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	root, err := os.OpenRoot(temporary)
	if err != nil {
		return err
	}
	for _, artifact := range m.Artifacts {
		if artifact.Platform == platform {
			_, _, err = inspectArchive(dir, m.Version, artifact, root)
			break
		}
	}
	closeErr := root.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	currentExists, err := emptyOutput(absolute)
	if err != nil || currentExists != exists {
		return fmt.Errorf("extraction output changed during verification: %v", err)
	}
	if !exists {
		// Mkdir is exclusive. Never rename over a path that another operation
		// created while we were validating the archive.
		if err := os.Mkdir(absolute, 0755); err != nil {
			return err
		}
	}
	outputRoot, err := os.OpenRoot(absolute)
	if err != nil {
		if !exists {
			os.Remove(absolute)
		}
		return err
	}
	defer outputRoot.Close()
	name := PackageName(m.Version, platform)
	// Reserve the only package-root entry exclusively. Commit with exclusive,
	// rooted writes instead of a platform-dependent replacing directory rename.
	if err := outputRoot.Mkdir(name, 0755); err != nil {
		if !exists {
			os.Remove(absolute)
		}
		return err
	}
	err = copyPayload(temporary, outputRoot)
	if err != nil {
		outputRoot.RemoveAll(name)
		if !exists {
			os.Remove(absolute)
		}
		return err
	}
	return nil
}

func copyPayload(source string, destination *os.Root) error {
	return filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, name)
		if err != nil || relative == "." {
			return err
		}
		if entry.IsDir() {
			return destination.MkdirAll(relative, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		input, err := openRegular(name, maxBinary)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := destination.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func plainDirectory(name string) error {
	for {
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("output parent must contain only real directories: %s", name)
		}
		parent := filepath.Dir(name)
		if parent == name {
			return nil
		}
		name = parent
	}
}

func emptyOutput(name string) (bool, error) {
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return true, fmt.Errorf("output is not a real directory")
	}
	entries, err := os.ReadDir(name)
	if err != nil {
		return true, err
	}
	if len(entries) != 0 {
		return true, fmt.Errorf("refusing nonempty extraction output")
	}
	return true, nil
}
