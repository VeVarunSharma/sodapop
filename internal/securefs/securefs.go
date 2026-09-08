package securefs

import (
	"errors"
	"os"
)

// MkdirAllPrivate creates a directory tree and restricts the final directory
// to the current user and platform administrators.
func MkdirAllPrivate(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private path must be a directory, not a link or special file")
	}
	return protectPath(path, true)
}

// ProtectFile restricts an open file to the current user and platform
// administrators.
func ProtectFile(file *os.File) error {
	if file == nil {
		return errors.New("private file is required")
	}
	return protectFile(file)
}

// IsPrivateRegularFile reports whether file is a regular file whose platform
// permissions prevent access by other ordinary users.
func IsPrivateRegularFile(file *os.File) (bool, error) {
	if file == nil {
		return false, errors.New("private file is required")
	}
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	return isPrivateFile(file, info)
}
