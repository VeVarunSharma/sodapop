//go:build windows

package workspace

import "os"

func openPreviewFile(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY, 0)
}
