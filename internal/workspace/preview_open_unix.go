//go:build darwin || linux

package workspace

import (
	"os"
	"syscall"
)

func openPreviewFile(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
