//go:build windows

package engine

import "os"

func syncIndexDirectory(*os.Root) error {
	// Windows does not expose a portable directory fsync through os.File.
	return nil
}
