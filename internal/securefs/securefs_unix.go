//go:build !windows

package securefs

import (
	"os"
)

func protectPath(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}

func protectFile(file *os.File) error {
	return file.Chmod(0600)
}

func isPrivateFile(_ *os.File, info os.FileInfo) (bool, error) {
	return info.Mode().Perm()&0077 == 0, nil
}
