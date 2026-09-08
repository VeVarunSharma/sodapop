//go:build !windows

package engine

import (
	"errors"
	"os"
)

func syncIndexDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
