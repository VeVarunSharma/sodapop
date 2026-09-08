//go:build !darwin && !linux && !windows

package workspace

import (
	"errors"
	"os"
)

func openPreviewFile(*os.Root, string) (*os.File, error) {
	return nil, errors.New("untracked file previews are unsupported on this platform")
}
