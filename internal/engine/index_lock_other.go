//go:build !darwin && !linux && !windows

package engine

import (
	"context"
	"errors"
	"os"
)

func lockIndex(context.Context, *os.Root) (*os.File, error) {
	return nil, errors.New("Sodapop session storage is supported only on macOS, Linux, and Windows")
}
